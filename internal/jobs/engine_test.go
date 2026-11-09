package jobs

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/apexo/pbmanager/internal/store"
)

func testEngine(t *testing.T, handlers map[string]Handler) (*Engine, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := New(st, slog.New(slog.NewTextHandler(testWriter{t}, nil)), handlers)
	e.interval = 5 * time.Millisecond
	return e, st
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}

func waitStatus(t *testing.T, st *store.Store, id string, want ...string) *store.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := st.GetJob(context.Background(), id)
		if err == nil {
			for _, status := range want {
				if job.Status == status {
					return job
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	job, _ := st.GetJob(context.Background(), id)
	t.Fatalf("job did not reach %v; last=%#v", want, job)
	return nil
}

func startEngine(t *testing.T, e *Engine) (context.CancelFunc, chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.Start(ctx)
	}()
	return cancel, done
}

func TestJobSuccessAndIdempotency(t *testing.T) {
	e, st := testEngine(t, map[string]Handler{
		"test.ok": func(ctx context.Context, ex *Execution) (any, error) {
			return map[string]string{"value": "yes"}, nil
		},
	})
	cancel, done := startEngine(t, e)
	defer func() { cancel(); <-done }()
	job, created, err := e.Enqueue(context.Background(), "test.ok", map[string]string{"a": "b"}, EnqueueOptions{IdempotencyKey: "key-1", CreatedBy: "test"})
	if err != nil || !created {
		t.Fatal(err)
	}
	dup, created, err := e.Enqueue(context.Background(), "test.ok", nil, EnqueueOptions{IdempotencyKey: "key-1"})
	if err != nil || created || dup.ID != job.ID {
		t.Fatalf("idempotency failed: %v %v %#v", err, created, dup)
	}
	final := waitStatus(t, st, job.ID, "succeeded")
	if final.Result == "" || final.Progress != 100 {
		t.Fatalf("final: %#v", final)
	}
}

func TestRetryThenSuccess(t *testing.T) {
	attempts := 0
	e, st := testEngine(t, map[string]Handler{
		"test.retry": func(ctx context.Context, ex *Execution) (any, error) {
			attempts++
			if attempts == 1 {
				return nil, RetryableAfter(errors.New("temporary"), time.Millisecond)
			}
			return "ok", nil
		},
	})
	cancel, done := startEngine(t, e)
	defer func() { cancel(); <-done }()
	job, _, _ := e.Enqueue(context.Background(), "test.retry", nil, EnqueueOptions{MaxAttempts: 3})
	final := waitStatus(t, st, job.ID, "succeeded")
	if final.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", final.Attempts)
	}
	if attempts != 2 {
		t.Fatalf("handler attempts = %d", attempts)
	}
}

func TestPermanentFailure(t *testing.T) {
	e, st := testEngine(t, map[string]Handler{
		"test.fail": func(ctx context.Context, ex *Execution) (any, error) {
			return nil, errors.New("permanent")
		},
	})
	cancel, done := startEngine(t, e)
	defer func() { cancel(); <-done }()
	job, _, _ := e.Enqueue(context.Background(), "test.fail", nil, EnqueueOptions{MaxAttempts: 5})
	final := waitStatus(t, st, job.ID, "failed")
	if final.LastError != "permanent" || final.Attempts != 1 {
		t.Fatalf("final: %#v", final)
	}
}

func TestRecoveryRequeuesRunningJob(t *testing.T) {
	e, st := testEngine(t, map[string]Handler{"test.recover": func(ctx context.Context, ex *Execution) (any, error) { return "ok", nil }})
	job, _, _ := e.Enqueue(context.Background(), "test.recover", nil, EnqueueOptions{})
	if _, err := st.ClaimNextJob(context.Background()); err != nil {
		t.Fatal(err)
	}
	ids, err := e.Recover(context.Background())
	if err != nil || len(ids) != 1 {
		t.Fatalf("recover: %v %v", ids, err)
	}
	recovered, _ := st.GetJob(context.Background(), job.ID)
	if recovered.Status != "queued" || recovered.RecoveredCount != 1 {
		t.Fatalf("recovered: %#v", recovered)
	}
}

func TestCancelRunningJob(t *testing.T) {
	started := make(chan struct{})
	e, st := testEngine(t, map[string]Handler{
		"test.cancel": func(ctx context.Context, ex *Execution) (any, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	cancel, done := startEngine(t, e)
	defer func() { cancel(); <-done }()
	job, _, _ := e.Enqueue(context.Background(), "test.cancel", nil, EnqueueOptions{})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start")
	}
	if err := e.Cancel(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	final := waitStatus(t, st, job.ID, "canceled")
	if !final.Canceled {
		t.Fatalf("not marked canceled: %#v", final)
	}
}

func TestStepCheckpointSurvivesRetry(t *testing.T) {
	runs := 0
	e, st := testEngine(t, map[string]Handler{
		"test.step": func(ctx context.Context, ex *Execution) (any, error) {
			err := ex.Step(ctx, "once", "only once", func(ctx context.Context) (string, error) {
				runs++
				return "done", nil
			})
			if err != nil {
				return nil, err
			}
			return nil, RetryableAfter(errors.New("retry after checkpoint"), time.Millisecond)
		},
	})
	cancel, done := startEngine(t, e)
	defer func() { cancel(); <-done }()
	job, _, _ := e.Enqueue(context.Background(), "test.step", nil, EnqueueOptions{MaxAttempts: 2})
	final := waitStatus(t, st, job.ID, "failed")
	if final.Attempts != 2 {
		t.Fatalf("attempts = %d", final.Attempts)
	}
	if runs != 1 {
		t.Fatalf("step ran %d times, want 1", runs)
	}
}
