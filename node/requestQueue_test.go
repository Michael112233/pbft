package node

import (
	"reflect"
	"testing"

	"github.com/michael112233/pbft/core"
)

func requestWithID(id int64) core.ClientMsgSignature {
	return core.ClientMsgSignature{Data: core.ClientMsg{Id: id}}
}

func TestRequestQueueIsBoundedAndPreservesFailedBatch(t *testing.T) {
	queue := NewRequestQueue(2)
	if !queue.Enqueue(requestWithID(1)) || !queue.Enqueue(requestWithID(2)) {
		t.Fatal("enqueue within capacity failed")
	}
	if queue.Enqueue(requestWithID(3)) {
		t.Fatal("enqueue succeeded after queue reached capacity")
	}

	batch := queue.PeekBatch(2)
	if len(batch) != 2 || batch[0].Data.Id != 1 || batch[1].Data.Id != 2 {
		t.Fatalf("unexpected batch: %+v", batch)
	}
	if queue.Len() != 2 {
		t.Fatalf("peek removed requests: queue length = %d, want 2", queue.Len())
	}

	// Simulate a failed proposal by not committing the peeked batch.
	retry := queue.PeekBatch(2)
	if retry[0].Data.Id != 1 || retry[1].Data.Id != 2 {
		t.Fatalf("failed proposal changed queued requests: %+v", retry)
	}
}

func TestRequestQueueCommitAndWrapAround(t *testing.T) {
	queue := NewRequestQueue(3)
	queue.Enqueue(requestWithID(1))
	queue.Enqueue(requestWithID(2))
	queue.Enqueue(requestWithID(3))

	if !queue.CommitBatch(2) {
		t.Fatal("valid batch commit failed")
	}
	if !queue.Enqueue(requestWithID(4)) || !queue.Enqueue(requestWithID(5)) {
		t.Fatal("enqueue after commit failed")
	}

	batch := queue.PeekBatch(3)
	for i, want := range []int64{3, 4, 5} {
		if batch[i].Data.Id != want {
			t.Fatalf("batch[%d].id = %d, want %d", i, batch[i].Data.Id, want)
		}
	}
	if queue.CommitBatch(4) {
		t.Fatal("invalid batch commit succeeded")
	}
	if queue.Len() != 3 {
		t.Fatalf("invalid commit changed queue length to %d", queue.Len())
	}
}

func TestRequestQueueDequeueInFIFOOrder(t *testing.T) {
	queue := NewRequestQueue(4)
	queue.Enqueue(requestWithID(1))
	queue.Enqueue(requestWithID(2))
	queue.Enqueue(requestWithID(3))

	first := queue.Dequeue(2)
	for i, want := range []int64{1, 2} {
		if first[i].Data.Id != want {
			t.Fatalf("first[%d].id = %d, want %d", i, first[i].Data.Id, want)
		}
	}

	// These enqueues wrap around to slots released by the first dequeue.
	queue.Enqueue(requestWithID(4))
	queue.Enqueue(requestWithID(5))

	remaining := queue.Dequeue(10)
	if len(remaining) != 3 {
		t.Fatalf("dequeued %d requests, want 3", len(remaining))
	}
	for i, want := range []int64{3, 4, 5} {
		if remaining[i].Data.Id != want {
			t.Fatalf("remaining[%d].id = %d, want %d", i, remaining[i].Data.Id, want)
		}
	}
	if queue.Len() != 0 {
		t.Fatalf("queue length = %d, want 0", queue.Len())
	}
	if requests := queue.Dequeue(0); requests != nil {
		t.Fatalf("Dequeue(0) = %+v, want nil", requests)
	}
}

func TestRequestQueueResetClearsWrappedQueueAndPreservesCapacity(t *testing.T) {
	queue := NewRequestQueue(3)
	queue.Enqueue(requestWithID(1))
	queue.Enqueue(requestWithID(2))
	queue.Dequeue(1)
	queue.Enqueue(requestWithID(3))
	queue.Enqueue(requestWithID(4))

	queue.Reset()

	if queue.Len() != 0 {
		t.Fatalf("queue length after reset = %d, want 0", queue.Len())
	}
	if queue.Capacity() != 3 {
		t.Fatalf("queue capacity after reset = %d, want 3", queue.Capacity())
	}
	if queue.Full() {
		t.Fatal("queue reports full after reset")
	}
	for i, req := range queue.requests {
		if !reflect.DeepEqual(req, core.ClientMsgSignature{}) {
			t.Fatalf("queue slot %d was not cleared: %+v", i, req)
		}
	}
	if !queue.Enqueue(requestWithID(5)) {
		t.Fatal("enqueue after reset failed")
	}
	batch := queue.Dequeue(1)
	if len(batch) != 1 || batch[0].Data.Id != 5 {
		t.Fatalf("unexpected batch after reset: %+v", batch)
	}
}
