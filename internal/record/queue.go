package record

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Sink persists batches of events. Implementations must not be called from the proxy hot path.
type Sink interface {
	WriteBatch(ctx context.Context, events []Event) error
}

// Queue is a bounded async event pipeline: Emit never blocks; full queue drops events.
type Queue struct {
	ch         chan Event
	sink       Sink
	batchN     int
	flushEvery time.Duration

	dropped atomic.Uint64
	written atomic.Uint64
	closed  atomic.Bool

	stopOnce sync.Once
	wg       sync.WaitGroup
}

// Options configures the ingest queue.
type Options struct {
	// Buffer is channel capacity (default 4096).
	Buffer int
	// BatchSize flushes when this many events accumulate (default 200).
	BatchSize int
	// FlushInterval flushes at least this often (default 1s).
	FlushInterval time.Duration
}

// NewQueue creates a queue. Call Start then Emit; Close flushes remaining events.
func NewQueue(sink Sink, opt Options) *Queue {
	if opt.Buffer <= 0 {
		opt.Buffer = 4096
	}
	if opt.BatchSize <= 0 {
		opt.BatchSize = 200
	}
	if opt.FlushInterval <= 0 {
		opt.FlushInterval = time.Second
	}
	return &Queue{
		ch:         make(chan Event, opt.Buffer),
		sink:       sink,
		batchN:     opt.BatchSize,
		flushEvery: opt.FlushInterval,
	}
}

// Start runs the background batch writer until Close.
func (q *Queue) Start() {
	if q == nil {
		return
	}
	q.wg.Add(1)
	go q.loop()
}

// Emit enqueues e without blocking. Drops when full or after Close (fail-open for pull path).
func (q *Queue) Emit(e Event) {
	if q == nil || q.closed.Load() {
		return
	}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	select {
	case q.ch <- e:
	default:
		q.dropped.Add(1)
	}
}

// Dropped returns the number of events discarded because the buffer was full.
func (q *Queue) Dropped() uint64 {
	if q == nil {
		return 0
	}
	return q.dropped.Load()
}

// Written returns events successfully handed to the sink.
func (q *Queue) Written() uint64 {
	if q == nil {
		return 0
	}
	return q.written.Load()
}

// Close stops the worker, drains the channel, and flushes a final batch.
// Safe to call once; subsequent Emit calls are no-ops.
func (q *Queue) Close() {
	if q == nil {
		return
	}
	q.stopOnce.Do(func() {
		q.closed.Store(true)
		close(q.ch)
		q.wg.Wait()
	})
}

func (q *Queue) loop() {
	defer q.wg.Done()
	batch := make([]Event, 0, q.batchN)
	ticker := time.NewTicker(q.flushEvery)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 || q.sink == nil {
			batch = batch[:0]
			return
		}
		out := make([]Event, len(batch))
		copy(out, batch)
		batch = batch[:0]
		if err := q.sink.WriteBatch(context.Background(), out); err != nil {
			log.Printf("[record] write batch (%d): %v", len(out), err)
			return
		}
		q.written.Add(uint64(len(out)))
	}

	for {
		select {
		case e, ok := <-q.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, e)
			if len(batch) >= q.batchN {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}
