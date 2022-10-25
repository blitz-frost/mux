// Package mux defines higher level utilities for mutually exclusive code.
package mux

import (
	"sync"
)

type Locker = sync.Locker
type S = sync.Mutex
type RW = sync.RWMutex

// A Path represents a synchronous code path.
//
// Functions that don't want to deal with deadlock analysis should take a Path as a parameter and use a Guard on it to prevent parallel executon.
// This comes at a performance penalty compared to using a standard Mutex.
//
// A top-level caller should obtain a unique Path through MakePath(), and then release it through the Release() method.
//
// The zero Path is invalid. Guards will always lock it.
//
// A specific Path must never be used asynchronously in multiple places. For example: for a Guard g and Path p, it must never happen that the method calls g.Guard(p) and g.Unguard(p) be in a race condition.
type Path int

// MakePath returns a usable unique Path.
func MakePath() Path {
	mux.Lock()
	defer mux.Unlock()

	for {
		seek++
		if seek == 0 {
			seek++
		}
		if _, ok := paths[seek]; !ok {
			break
		}
	}

	paths[seek] = struct{}{}
	return seek
}

// Release frees the Path for reuse.
func (x Path) Release() {
	mux.Lock()
	defer mux.Unlock()

	delete(paths, x)
}

type Guarder interface {
	Guard(Path)
	Unguard(Path)
}

var (
	paths = make(map[Path]struct{})
	seek  Path // last allocated Path
	mux   S
)

// A Guard allows only one Path to proceed execution at a time.
// Must not be copied after first use.
type Guard struct {
	mux   S    // underlying mutex
	path  Path // currently guarded Path
	locks int  // guard counter
}

// Guard will only lock the underlying mutex if the given Path isn't already holding it.
// Otherwise increments its lock count.
//
// Guarding is slightly slower than direct locking.
func (x *Guard) Guard(p Path) {
	if p != x.path {
		x.mux.Lock()
		x.path = p
		x.locks = 1
	} else {
		x.locks++
	}
}

// Lock directly locks the underlying mutex.
func (x *Guard) Lock() {
	x.mux.Lock()
}

// Unguard unlocks the underlying mutex if the given Path has only one outstanding lock on it.
// Otherwise decrements its lock count.
//
// Calling Unguard on a Path that is not currently holding the lock will result in undefined behaviour (corrupt Guard).
func (x *Guard) Unguard(p Path) {
	if x.locks == 1 {
		x.path = 0
		x.locks = 0
		x.mux.Unlock()
	} else {
		x.locks--
	}
}

// Unlock directly unlocks the underlying mutex.
func (x *Guard) Unlock() {
	x.mux.Unlock()
}

// A RWGuard allows multiple Paths to obtain read locks.
// Only one Path may obtain a full lock, which is mutually exclusive with read locks, other than its own.
//
// If a Path holds only a read lock and attempts to obtain a full lock, it will panic. Id est:
// Paths that start with Guard may recursively call Guard or RGuard any number of times.
// Paths that start with RGuard may only recuresively call RGuard.
//
// The zero RWGuard is not valid.
//
// Must not be copied after first use.
type RWGuard struct {
	mux    RW    // underlying mutex
	wPath  Path  // full lock holder
	wLocks int   // full guard counter
	rLocks locks // read guard counters
}

func MakeRWGuard() RWGuard {
	return RWGuard{
		rLocks: makeLocks(),
	}
}

func (x *RWGuard) Guard(p Path) {
	if p != x.wPath {
		if x.rLocks.get(p) > 0 {
			panic("guard promotion")
		}

		x.mux.Lock()
		x.wPath = p
		x.wLocks = 1
	} else {
		x.wLocks++
	}
}

// Lock directly locks the underlying mutex.
func (x *RWGuard) Lock() {
	x.mux.Lock()
}

func (x *RWGuard) RGuard(p Path) {
	// hold only one read lock
	// read lock + unlock is slower than an if
	if x.rLocks.inc(p) && p != x.wPath {
		x.mux.RLock()
	}
}

// RLock directly read locks the underlying mutex.
func (x *RWGuard) RLock() {
	x.mux.RLock()
}

func (x *RWGuard) RUnguard(p Path) {
	if x.rLocks.dec(p) && p != x.wPath {
		x.mux.RUnlock()
	}
}

func (x *RWGuard) RUnlock() {
	x.mux.RUnlock()
}

func (x *RWGuard) Unguard(p Path) {
	x.wLocks--
	if x.wLocks == 0 {
		x.wPath = 0
		x.mux.Unlock()
	}
}

func (x *RWGuard) Unlock() {
	x.mux.Unlock()
}

type locks struct {
	m   map[Path]int
	mux S
}

func makeLocks() locks {
	return locks{
		m: make(map[Path]int),
	}
}

// dec decrements p's value.
// Deletes p if it reaches 0.
// Returns true if p was deleted.
func (x *locks) dec(p Path) bool {
	x.mux.Lock()
	defer x.mux.Unlock()

	n := x.m[p]
	if n == 1 {
		delete(x.m, p)
		return true
	}

	x.m[p] = n - 1
	return false
}

func (x *locks) get(p Path) int {
	x.mux.Lock()
	defer x.mux.Unlock()

	return x.m[p]
}

// inc increments p's value.
// Returns true if p did not exist (is now 1).
func (x *locks) inc(p Path) bool {
	x.mux.Lock()
	defer x.mux.Unlock()

	n := x.m[p]
	x.m[p] = n + 1

	return n == 0
}
