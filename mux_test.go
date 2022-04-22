package mux

import (
	"math/rand"
	"sync"
	"testing"
)

func BenchmarkS(b *testing.B) {
	mux := S{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mux.Lock()
		mux.Unlock()
	}
}

func BenchmarkGuard(b *testing.B) {
	mux := Guard{}
	p := Path(1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mux.Guard(p)
		mux.Unguard(p)
	}
}

func BenchmarkRWWrite(b *testing.B) {
	mux := RW{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mux.Lock()
		mux.Unlock()
	}
}

func BenchmarkRWRead(b *testing.B) {
	mux := RW{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mux.RLock()
		mux.RUnlock()
	}
}

func BenchmarkRWGuardWrite(b *testing.B) {
	mux := MakeRWGuard()
	p := Path(1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mux.Guard(p)
		mux.Unguard(p)
	}
}

func BenchmarkRWGuardRead(b *testing.B) {
	mux := MakeRWGuard()
	p := Path(1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mux.RGuard(p)
		mux.RUnguard(p)
	}
}

type testEvent int

func (x testEvent) isLock() bool {
	if x == testEventGuard || x == testEventLock || x == testEventRGuard || x == testEventRLock {
		return true
	}
	return false
}

func (x testEvent) String() string {
	switch x {
	case testEventGuard:
		return "Guard"
	case testEventLock:
		return "Lock"
	case testEventRGuard:
		return "Read Guard"
	case testEventRLock:
		return "Read Lock"
	case testEventRUnguard:
		return "Read Unguard"
	case testEventRUnlock:
		return "Read Unlock"
	case testEventUnguard:
		return "Unguard"
	case testEventUnlock:
		return "Unlock"
	case testEventPanic:
		return "Panic"
	}
	return "invalid event"
}

const (
	testEventGuard testEvent = iota
	testEventLock
	testEventRGuard
	testEventRLock
	testEventRUnguard
	testEventRUnlock
	testEventUnguard
	testEventUnlock
	testEventPanic
)

type testRecord struct {
	path  Path
	event testEvent
}

type testResult struct {
	s   []testRecord
	mux sync.Mutex
}

func (x *testResult) add(r testRecord) {
	x.mux.Lock()
	defer x.mux.Unlock()

	x.s = append(x.s, r)
}

func TestGuard(t *testing.T) {
	guard := MakeRWGuard() // test target
	res := testResult{}

	type eventCollection struct {
		fn     []func(p Path)
		index  map[testEvent]int
		events []testEvent
	}

	eventCollectionMake := func() eventCollection {
		return eventCollection{
			index: make(map[testEvent]int),
		}
	}
	eventCollectionAdd := func(x *eventCollection, e testEvent, recordAfter bool, f func(p Path)) {
		i := len(x.events)
		var fn func(Path)
		if recordAfter {
			fn = func(p Path) {
				f(p)
				res.add(testRecord{p, e})
			}
		} else {
			fn = func(p Path) {
				res.add(testRecord{p, e})
				f(p)
			}
		}
		x.fn = append(x.fn, fn)
		x.events = append(x.events, e)
		x.index[e] = i
	}

	// test sequence lock functions
	lockCol := eventCollectionMake()
	eventCollectionAdd(&lockCol, testEventGuard, true, guard.Guard)
	eventCollectionAdd(&lockCol, testEventLock, true, func(p Path) { guard.Lock() })
	eventCollectionAdd(&lockCol, testEventRGuard, true, guard.RGuard)
	eventCollectionAdd(&lockCol, testEventRLock, true, func(p Path) { guard.RLock() })

	// corresponding unlock functions
	unlockCol := eventCollectionMake()
	eventCollectionAdd(&unlockCol, testEventUnguard, false, guard.Unguard)
	eventCollectionAdd(&unlockCol, testEventUnlock, false, func(p Path) { guard.Unlock() })
	eventCollectionAdd(&unlockCol, testEventRUnguard, false, guard.RUnguard)
	eventCollectionAdd(&unlockCol, testEventRUnlock, false, func(p Path) { guard.RUnlock() })

	// used to map from integer range to testEvent constants
	options := map[testEvent][]int{
		testEventLock:   nil, // standard lock/rlock must not be recursive in any combination
		testEventRLock:  nil,
		testEventGuard:  []int{lockCol.index[testEventGuard], lockCol.index[testEventRGuard]},
		testEventRGuard: []int{lockCol.index[testEventRGuard]},
	}
	optionsAll := []int{lockCol.index[testEventGuard], lockCol.index[testEventRGuard], lockCol.index[testEventLock], lockCol.index[testEventRLock]}

	evalOptions := func(events map[testEvent]int) []int {
		// Guard and RLock are mutually exclusive
		if events[testEventLock] > 0 {
			return options[testEventLock]
		} else if events[testEventRLock] > 0 {
			return options[testEventRLock]
		} else if events[testEventGuard] > 0 {
			return options[testEventGuard]
		} else if events[testEventRGuard] > 0 {
			return options[testEventRGuard]
		}
		return optionsAll
	}

	type testAction struct {
		event testEvent
		fn    func(Path)
	}

	// test path sequences
	paths := make([][]testAction, 100)
	for i := range paths {
		// determine sequence size
		n := rand.Intn(10) + 1 // arbitrary max size
		seq := make([]testAction, 0, 2*n)
		var locks []int                   // locks that need to be unlocked
		events := make(map[testEvent]int) // individual event tracking
		options := optionsAll             // current available options

		// randomly selection a lock option, or unlock the latest lock
		for i := 0; i < n; i++ {
			m := len(options)
			j := rand.Intn(m + 1)

			// unlock on m, or just try again if nothing to unlock
			if j == m {
				if k := len(locks) - 1; k >= 0 {
					opt := locks[k]
					seq = append(seq, testAction{
						event: unlockCol.events[opt],
						fn:    unlockCol.fn[opt],
					})
					locks = locks[:k]

					// adjust options
					ev := lockCol.events[opt] // lock event that got unlocked
					had := events[ev]
					events[ev] = had - 1
					if had == 1 {
						options = evalOptions(events)
					}
				}
				i-- // unlocks don't count towards the quota
				continue
			}

			opt := options[j]
			ev := lockCol.events[opt]
			seq = append(seq, testAction{
				event: ev,
				fn:    lockCol.fn[opt],
			})
			locks = append(locks, opt)

			// adjust options
			had := events[ev]
			events[ev] = had + 1
			if had == 0 {
				options = evalOptions(events)
			}
		}

		// add all remaining unlocks
		for k := len(locks) - 1; k >= 0; k-- {
			opt := locks[k]
			seq = append(seq, testAction{
				event: unlockCol.events[opt],
				fn:    unlockCol.fn[opt],
			})
		}

		paths[i] = seq
	}

	// execute all paths concurrently
	wg := sync.WaitGroup{}
	for i, seq := range paths {
		wg.Add(1)
		go func(p Path, seq []testAction) {
			var i int
			defer func() {
				wg.Done()
			}()
			defer func() {
				if x := recover(); x != nil {
					res.add(testRecord{
						path:  p,
						event: testEventPanic,
					})

					// release dangling locks

					// first chart them
					var locks []testEvent
					for j := 0; j < i; j++ {
						ev := seq[j].event
						if ev.isLock() {
							locks = append(locks, ev)
						} else {
							locks = locks[:len(locks)-1]
						}
					}

					// release in LIFO order
					for j := len(locks) - 1; j >= 0; j-- {
						index := lockCol.index[locks[j]]
						unlockCol.fn[index](p)
					}
				}
			}()
			for i = range seq {
				seq[i].fn(p)
			}
		}(Path(i+1), seq) // 0 is an invalid Path
	}
	wg.Wait()

	// validate a record at position i in the result records
	// uses "check" function to determine if a subsequent record corresponds to expectations
	// record is valid if end is reached without "check" returning false
	validate := func(i int, end testEvent, check func(r testRecord) bool) bool {
		start := res.s[i].event
		path := res.s[i].path
		dup := 0 // duplicate start event

		for j := i + 1; j < len(res.s); j++ {
			r := res.s[j]

			// panics are universally valid path endings
			if r.event == testEventPanic {
				if r.path == path {
					return true
				} else {
					continue // skip other path panics
				}
			}

			if !check(r) {
				t.Error(r.path, r.event, "before", path, end)
				return false
			}

			if r.path == path {
				if r.event == start {
					dup++
				} else if r.event == end {
					if dup == 0 {
						return true
					}
					dup--
				}
			}
		}

		t.Error(path, start, "without", end)
		return false
	}

	// validate result
	for i, r := range res.s {
		p := r.path
		var (
			check func(r testRecord) bool
			end   testEvent
		)
		switch r.event {
		case testEventGuard:
			// must only find own Guard, Unguard, RGuard and RUnguard
			valid := map[testEvent]bool{
				testEventGuard:    true,
				testEventUnguard:  true,
				testEventRGuard:   true,
				testEventRUnguard: true,
			}
			check = func(r testRecord) bool {
				if !valid[r.event] || r.path != p {
					return false
				}
				return true
			}
			end = testEventUnguard
		case testEventRGuard:
			// must find own Guard, Unguard, RGuard, RUnguard, RLock, RUnlock
			// or RGuard, RUnguard, RLock, RUnlock from others
			validOwn := map[testEvent]bool{
				testEventGuard:    true,
				testEventUnguard:  true,
				testEventRGuard:   true,
				testEventRUnguard: true,
				testEventRLock:    true,
				testEventRUnlock:  true,
			}
			validOther := map[testEvent]bool{
				testEventRGuard:   true,
				testEventRUnguard: true,
				testEventRLock:    true,
				testEventRUnlock:  true,
			}
			check = func(r testRecord) bool {
				if r.path == p {
					if !validOwn[r.event] {
						return false
					}
				} else {
					if !validOther[r.event] {
						return false
					}
				}
				return true
			}
			end = testEventRUnguard
		case testEventLock:
			// must find nothing but the corresponding Unlock
			check = func(r testRecord) bool {
				if !(r.event == testEventUnlock && r.path == p) {
					return false
				}
				return true
			}
			end = testEventUnlock
		case testEventRLock:
			// must find only RGuard, RUnguard, RLock or RUnlock events of any path
			valid := map[testEvent]bool{
				testEventRGuard:   true,
				testEventRUnguard: true,
				testEventRLock:    true,
				testEventRUnlock:  true,
			}
			check = func(r testRecord) bool {
				if !valid[r.event] {
					return false
				}
				return true
			}
			end = testEventRUnlock
		default:
			// skip unlock events
			continue
		}

		if !validate(i, end, check) {
			return
		}
	}
}
