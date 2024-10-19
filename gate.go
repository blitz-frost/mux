package mux

import "errors"

var (
	ErrCanceled = errors.New("canceled")
	ErrConflict = errors.New("deadlock conflict")
)

type Thread struct {
	// Can be used to decide how to settle conflicts between Threads.
	// Note that during a conflict, it is guaranteed that the conflicting Thread has exclusive access to this member.
	// Since other Threads can only enter a conflict with a Thread that is attempting to Lock a Gate, it is also guaranteed that a Thread has exclusive access to its own Data member.
	Data any

	mux     S          // protect own state
	waiting *Gate      // Gate currently blocked on, if any
	cancel  chan error // used to cancel waiting
}

func ThreadMake(data any) *Thread {
	return &Thread{
		Data:   data,
		cancel: make(chan error),
	}
}

// Cancels resolves the Thread's current conflict by canceling its Gate locking attempt.
// The reason may be nil, in which case a generic one will be supplied to the blocked side.
func (x *Thread) Cancel(reason error) {
	x.waiting = nil
	x.cancel <- reason
	x.unlock()
}

// Resolve resolves the Thread's current conflict by allowing it to continue waiting.
// The caller should then abort what it's doing and unwind, unblocking the Key.
func (x *Thread) Resolve() {
	x.unlock()
}

func (x *Thread) lock() {
	x.mux.Lock()
}

func (x *Thread) unlock() {
	x.mux.Unlock()
}

type Gate struct {
	thread *Thread
	count  int
	wait   chan struct{}
	mux    S
}

func GateMake() *Gate {
	return &Gate{
		wait: make(chan struct{}),
	}
}

// Lock attempts to lock to a given Thread. The same Thread may Lock a Gate multiple times. It must then Unlock it an equal number of times.
//
// Returns a nil error on success.
// If locking would deadlock with another Thread, returns that Thread alongside ErrConflict. The conflict must be resolved by either calling the returned Thread's Cancel() or Resolve() method. See those methods for clarification about what the caller should do next.
//
// If another Thread resolves an encountered conflict by canceling this Thread, returns the provided cancelation reason, or ErrCanceled if none was provided.
func (x *Gate) Lock(t *Thread) (*Thread, error) {
	var o *Thread
	var err error

	var wait bool

	// a single Gate may evaluate a Thread at a time
	t.lock()
	x.mux.Lock()
	switch x.thread {
	case nil:
		x.thread = t
		fallthrough
	case t:
		x.count++
	default:
		// check for conflict
		x.thread.lock()
		if x.thread.waiting == nil {
			// no deadlock risk, just wait normaly
			x.thread.unlock()

			t.waiting = x
			wait = true
		} else {
			// potential conflict
			if x.thread.waiting.threadGet() == t {
				// the thread holding this lock is waiting on a gate held by the incoming thread
				// keep it locked until conflict is resolved
				o = x.thread
			} else {
				// waiting on something else, no deadlock
				x.thread.unlock()

				t.waiting = x
				wait = true
			}
		}
	}
	x.mux.Unlock()
	t.unlock()

	if wait {
		// wait to get unblocked
		select {
		case <-x.wait:
			// take ownership of gate
			// mutex is locked, count is 1
			x.thread = t
			x.mux.Unlock()

			// hypothetically another thread could erronously detect this thread as waiting during this gap
			// but the other thread would need to hold this gate's mutex while checking that, which is mutually exclusive with this gate getting unlocked (and so potentially unblocking this thread)
			// at worst they will see it waiting on a gate that is held by itself

			t.lock()
			t.waiting = nil
			t.unlock()
		case err = <-t.cancel:
			// must be able to distinguish from successful locking
			if err == nil {
				err = ErrCanceled
			}
		}
	}

	return o, err
}

// Unlock must be called by the goroutine associated with the currently owning Thread.
func (x *Gate) Unlock() {
	x.mux.Lock()
	if x.count == 1 {
		// unblock a waiting thread, if any
		select {
		case x.wait <- struct{}{}:
			// let the new thread unlock after updating the lock
			return
		default:
			x.thread = nil
			x.count = 0
		}
	}
	x.mux.Unlock()
}

func (x *Gate) threadGet() *Thread {
	x.mux.Lock()
	o := x.thread
	x.mux.Unlock()
	return o
}

func example() error {
	g := GateMake()
	t := ThreadMake(nil)

try:
	conflict, err := g.Lock(t)
	if err != nil {
		if err == ErrConflict {
			var decision bool
			// solve conflict, potentially using t.Data and conflict.Data

			if decision {
				// cancel other thread and try locking again

				conflict.Cancel(errors.New("some reason"))
				goto try
			} else {
				// yield to the other thread

				conflict.Resolve()
				return errors.New("abort")
			}

		} else {
			// this Thread got canceled; abort
			return err
		}
	}

	// do stuff

	g.Unlock()

	// carry on

	return nil
}
