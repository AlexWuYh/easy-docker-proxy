package record

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memSink struct {
	mu     sync.Mutex
	events []Event
	delay  time.Duration
}

type flakySink struct {
	failures int32
	calls    atomic.Int32
}

func (s *flakySink) WriteBatch(_ context.Context, _ []Event) error {
	if s.calls.Add(1) <= s.failures {
		return errors.New("temporary write failure")
	}
	return nil
}

func (m *memSink) WriteBatch(_ context.Context, events []Event) error {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	m.mu.Lock()
	m.events = append(m.events, events...)
	m.mu.Unlock()
	return nil
}

func (m *memSink) len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

func TestQueueFlushBatch(t *testing.T) {
	sink := &memSink{}
	q := NewQueue(sink, Options{Buffer: 64, BatchSize: 5, FlushInterval: 50 * time.Millisecond})
	q.Start()
	for i := 0; i < 5; i++ {
		q.Emit(Event{Registry: "r", EventType: EventManifest, Repository: "lib/x", Status: 200})
	}
	deadline := time.Now().Add(2 * time.Second)
	for sink.len() < 5 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	q.Close()
	if sink.len() != 5 {
		t.Fatalf("got %d events", sink.len())
	}
	if q.Written() != 5 {
		t.Fatalf("written %d", q.Written())
	}
}

func TestEmitNeverBlocksWhenFull(t *testing.T) {
	// Slow sink + tiny buffer forces drops without blocking Emit.
	sink := &memSink{delay: 200 * time.Millisecond}
	q := NewQueue(sink, Options{Buffer: 2, BatchSize: 100, FlushInterval: time.Hour})
	q.Start()
	defer q.Close()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			q.Emit(Event{Registry: "r", EventType: EventBlob, Repository: "x", Status: 200, Bytes: 1})
		}
		close(done)
	}()

	select {
	case <-done:
		// ok — Emit returned promptly
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Emit blocked on full queue")
	}
	if q.Dropped() == 0 {
		// May race if worker drained fast; still require non-blocking above.
		t.Log("warning: no drops observed (worker may have been fast)")
	}
}

func TestNilQueueEmit(t *testing.T) {
	var q *Queue
	q.Emit(Event{}) // must not panic
}

func TestDropCounter(t *testing.T) {
	// Sink that never starts consumer drain: don't Start, fill and drop.
	// Without Start, channel fills and drops.
	sink := &memSink{}
	q := NewQueue(sink, Options{Buffer: 2, BatchSize: 10, FlushInterval: time.Hour})
	// intentionally no Start
	var n atomic.Uint64
	for i := 0; i < 10; i++ {
		q.Emit(Event{Registry: "r", EventType: EventManifest, Repository: "a", Status: 200})
		n.Add(1)
	}
	if q.Dropped() < 1 {
		t.Fatalf("expected drops, dropped=%d", q.Dropped())
	}
	// Close without Start would hang Wait — only Close if started.
	// Start then close to drain.
	q.Start()
	q.Close()
}

func TestQueueRetriesTransientWriteFailure(t *testing.T) {
	sink := &flakySink{failures: 2}
	q := NewQueue(sink, Options{
		Buffer: 4, BatchSize: 2, FlushInterval: time.Hour,
		MaxWriteAttempts: 3, RetryBackoff: time.Millisecond,
	})
	q.Start()
	q.Emit(Event{Registry: "r", EventType: EventManifest, Repository: "a", Status: 200})
	q.Emit(Event{Registry: "r", EventType: EventBlob, Repository: "a", Status: 200})
	q.Close()

	if got := sink.calls.Load(); got != 3 {
		t.Fatalf("write attempts=%d want 3", got)
	}
	if q.Written() != 2 || q.Dropped() != 0 {
		t.Fatalf("written=%d dropped=%d", q.Written(), q.Dropped())
	}
}

func TestQueueCountsBatchDroppedAfterRetryExhaustion(t *testing.T) {
	sink := &flakySink{failures: 10}
	q := NewQueue(sink, Options{
		Buffer: 4, BatchSize: 2, FlushInterval: time.Hour,
		MaxWriteAttempts: 3, RetryBackoff: time.Millisecond,
	})
	q.Start()
	q.Emit(Event{Registry: "r", EventType: EventManifest, Repository: "a", Status: 200})
	q.Emit(Event{Registry: "r", EventType: EventBlob, Repository: "a", Status: 200})
	q.Close()

	if got := sink.calls.Load(); got != 3 {
		t.Fatalf("write attempts=%d want 3", got)
	}
	if q.Written() != 0 || q.Dropped() != 2 {
		t.Fatalf("written=%d dropped=%d", q.Written(), q.Dropped())
	}
}

func TestQueueStartIsIdempotent(t *testing.T) {
	sink := &memSink{}
	q := NewQueue(sink, Options{Buffer: 4, BatchSize: 1, FlushInterval: time.Hour})
	q.Start()
	q.Start()
	q.Emit(Event{Registry: "r", EventType: EventManifest, Repository: "a", Status: 200})
	q.Close()

	if sink.len() != 1 || q.Written() != 1 {
		t.Fatalf("events=%d written=%d", sink.len(), q.Written())
	}
}

func TestQueueConcurrentEmitAndClose(t *testing.T) {
	q := NewQueue(&memSink{}, Options{Buffer: 32, BatchSize: 8, FlushInterval: time.Millisecond})
	q.Start()

	var senders sync.WaitGroup
	for i := 0; i < 16; i++ {
		senders.Add(1)
		go func() {
			defer senders.Done()
			for j := 0; j < 200; j++ {
				q.Emit(Event{Registry: "r", EventType: EventManifest, Repository: "a", Status: 200})
			}
		}()
	}

	q.Close()
	senders.Wait()
	q.Emit(Event{})
}
