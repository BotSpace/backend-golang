package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestStart_RunsJobOnItsInterval(t *testing.T) {
	log := zap.NewNop()
	var ticks int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	Start(ctx, log, []Job{{
		Name:     "test-job",
		Interval: 10 * time.Millisecond,
		Run: func(context.Context) error {
			atomic.AddInt32(&ticks, 1)
			return nil
		},
	}})

	deadline := time.Now().Add(500 * time.Millisecond)
	for atomic.LoadInt32(&ticks) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&ticks); got < 2 {
		t.Fatalf("expected at least 2 ticks within 500ms, got %d", got)
	}
}

func TestStart_StopsWhenContextCancelled(t *testing.T) {
	log := zap.NewNop()
	var ticks int32

	ctx, cancel := context.WithCancel(context.Background())
	Start(ctx, log, []Job{{
		Name:     "test-job",
		Interval: 5 * time.Millisecond,
		Run: func(context.Context) error {
			atomic.AddInt32(&ticks, 1)
			return nil
		},
	}})

	time.Sleep(30 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)
	after := atomic.LoadInt32(&ticks)

	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&ticks); got != after {
		t.Fatalf("expected no more ticks after cancel, went from %d to %d", after, got)
	}
}

// TestTick_JobErrorDoesNotStopTheTicker: a failing tick must not break
// subsequent ticks — cleanup jobs hitting a transient DB error should retry
// on the next interval, not die silently.
func TestTick_JobErrorDoesNotStopTheTicker(t *testing.T) {
	log := zap.NewNop()
	var calls int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	Start(ctx, log, []Job{{
		Name:     "flaky-job",
		Interval: 10 * time.Millisecond,
		Run: func(context.Context) error {
			n := atomic.AddInt32(&calls, 1)
			if n == 1 {
				return errors.New("transient failure")
			}
			return nil
		},
	}})

	deadline := time.Now().Add(500 * time.Millisecond)
	for atomic.LoadInt32(&calls) < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&calls); got < 3 {
		t.Fatalf("expected the ticker to keep going after a job error, got %d calls", got)
	}
}

// TestTick_PanicIsRecovered: a job panicking must not take down the ticker
// goroutine (or the test process).
func TestTick_PanicIsRecovered(t *testing.T) {
	log := zap.NewNop()
	var calls int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	Start(ctx, log, []Job{{
		Name:     "panicky-job",
		Interval: 10 * time.Millisecond,
		Run: func(context.Context) error {
			n := atomic.AddInt32(&calls, 1)
			if n == 1 {
				panic("boom")
			}
			return nil
		},
	}})

	deadline := time.Now().Add(500 * time.Millisecond)
	for atomic.LoadInt32(&calls) < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&calls); got < 3 {
		t.Fatalf("expected the ticker to survive a panicking job, got %d calls", got)
	}
}
