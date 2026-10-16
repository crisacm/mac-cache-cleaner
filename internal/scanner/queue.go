package scanner

import "sync"

// dirTask is a directory to process together with the declared scan root it
// belongs to (needed for policy checks and device-crossing).
type dirTask struct {
	root string
	path string
}

// dirQueue is a bounded-worker work queue of directories. Goroutines are
// bounded by the number of workers; the queue itself never deadlocks because
// pushes and completions are tracked with a pending counter (SEC-COD-04).
type dirQueue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	items   []dirTask
	pending int
	closed  bool
}

func newDirQueue() *dirQueue {
	q := &dirQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push enqueues a directory and marks it pending.
func (q *dirQueue) push(task dirTask) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, task)
	q.pending++
	q.cond.Signal()
}

// pop returns the next directory, or ok=false when the queue is drained.
func (q *dirQueue) pop() (dirTask, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return dirTask{}, false
	}
	task := q.items[len(q.items)-1]
	q.items = q.items[:len(q.items)-1]
	return task, true
}

// done marks the current directory complete; when nothing is pending the queue
// closes and wakes all waiters.
func (q *dirQueue) done() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending--
	if q.pending == 0 {
		q.closed = true
		q.cond.Broadcast()
	}
}

// close wakes every waiter so workers blocked in pop return instead of waiting
// forever. It is used to abort the walk on cancellation; it is idempotent and
// safe to call after the queue has already drained.
func (q *dirQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cond.Broadcast()
}
