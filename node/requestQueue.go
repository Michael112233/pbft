package node

import "github.com/michael112233/pbft/core"

// RequestQueue is a bounded FIFO owned by the node event loop. It is not safe
// for concurrent use; all mutations should happen in Node.run.
type RequestQueue struct {
	requests []core.ClientMsgSignature
	head     int
	size     int
}

func NewRequestQueue(capacity int) RequestQueue {
	if capacity <= 0 {
		panic("request queue capacity must be positive")
	}

	return RequestQueue{
		requests: make([]core.ClientMsgSignature, capacity),
	}
}

func (q *RequestQueue) Len() int {
	return q.size
}

func (q *RequestQueue) Capacity() int {
	return len(q.requests)
}

func (q *RequestQueue) Full() bool {
	return q.size == len(q.requests)
}

// Reset removes all queued requests while preserving the queue's capacity.
func (q *RequestQueue) Reset() {
	clear(q.requests)
	q.head = 0
	q.size = 0
}

// Enqueue returns false when the queue is full. The event loop should avoid
// receiving from the client channel while Full reports true, so this is an
// invariant check rather than a request-dropping policy.
func (q *RequestQueue) Enqueue(req core.ClientMsgSignature) bool {
	if q.Full() {
		return false
	}

	tail := (q.head + q.size) % len(q.requests)
	q.requests[tail] = req
	q.size++
	return true
}

// Dequeue removes and returns up to max requests in FIFO order.
// It returns nil when max is non-positive or the queue is empty.
func (q *RequestQueue) Dequeue(max int) []core.ClientMsgSignature {
	if max <= 0 || q.size == 0 {
		return nil
	}
	if max > q.size {
		max = q.size
	}

	requests := make([]core.ClientMsgSignature, max)
	for i := range requests {
		index := (q.head + i) % len(q.requests)
		requests[i] = q.requests[index]
		q.requests[index] = core.ClientMsgSignature{}
	}

	q.head = (q.head + max) % len(q.requests)
	q.size -= max
	return requests
}

// PeekBatch returns a copy of up to max requests without removing them. A
// failed proposal therefore leaves the queue unchanged.
func (q *RequestQueue) PeekBatch(max int) []core.ClientMsgSignature {
	if max <= 0 || q.size == 0 {
		return nil
	}
	if max > q.size {
		max = q.size
	}

	batch := make([]core.ClientMsgSignature, max)
	for i := range batch {
		batch[i] = q.requests[(q.head+i)%len(q.requests)]
	}
	return batch
}

// CommitBatch removes count requests after the proposal layer has accepted
// the batch. It returns false and leaves the queue unchanged for an invalid
// count.
func (q *RequestQueue) CommitBatch(count int) bool {
	if count < 0 || count > q.size {
		return false
	}

	for i := 0; i < count; i++ {
		index := (q.head + i) % len(q.requests)
		q.requests[index] = core.ClientMsgSignature{}
	}
	q.head = (q.head + count) % len(q.requests)
	q.size -= count
	return true
}
