// Package jobs implements the durable background job engine.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/apexo/pbmanager/internal/store"
)

// Handler executes one job type. It should be idempotent and use Execution.Step
// for checkpoints.
type Handler func(ctx context.Context, ex *Execution) (any, error)

// RetryableError asks the engine to retry after bounded backoff.
type RetryableError struct {
	Err        error
	RetryAfter time.Duration
}

func (e *RetryableError) Error() string { return e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

// Retryable wraps an error for retry.
func Retryable(err error) error { return &RetryableError{Err: err} }

// RetryableAfter wraps an error with a minimum delay.
func RetryableAfter(err error, d time.Duration) error {
	return &RetryableError{Err: err, RetryAfter: d}
}

// EnqueueOptions controls a new job.
type EnqueueOptions struct {
	Priority       int
	MaxAttempts    int
	IdempotencyKey string
	CreatedBy      string
}

// Engine owns the single worker and handler registry.
type Engine struct {
	store    *store.Store
	log      *slog.Logger
	handlers map[string]Handler
	interval time.Duration

	mu       sync.Mutex
	cancel   map[string]context.CancelFunc
	shutdown bool
	started  bool
}

// New constructs an engine.
func New(st *store.Store, log *slog.Logger, handlers map[string]Handler) *Engine {
	if log == nil {
		log = slog.Default()
	}
	if handlers == nil {
		handlers = map[string]Handler{}
	}
	return &Engine{store: st, log: log, handlers: handlers, interval: time.Second, cancel: map[string]context.CancelFunc{}}
}

// Register adds or replaces a handler.
func (e *Engine) Register(name string, h Handler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handlers[name] = h
}

// Enqueue creates a job, returning an existing one for a duplicate idempotency
// key. created is false when deduplicated.
func (e *Engine) Enqueue(ctx context.Context, jobType string, payload any, opts EnqueueOptions) (*store.Job, bool, error) {
	if _, ok := e.handlers[jobType]; !ok {
		return nil, false, fmt.Errorf("unknown job type %q", jobType)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 4
	}
	job := &store.Job{
		Type: jobType, Payload: string(raw), Status: "queued", Priority: opts.Priority,
		MaxAttempts: maxAttempts, AvailableAt: store.Now(), IdempotencyKey: opts.IdempotencyKey,
		CreatedBy: opts.CreatedBy, CreatedAt: store.Now(),
	}
	return e.store.CreateJob(ctx, job)
}

// Recover requeues jobs interrupted by an unclean shutdown. Call before Start.
func (e *Engine) Recover(ctx context.Context) ([]string, error) {
	return e.store.RecoverRunningJobs(ctx)
}

// Start launches the worker. It blocks until ctx is canceled.
func (e *Engine) Start(ctx context.Context) {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return
	}
	e.started = true
	e.shutdown = false
	e.mu.Unlock()
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	for {
		// Drain runnable jobs before waiting.
		for {
			if ctx.Err() != nil {
				return
			}
			ran, err := e.runNext(ctx)
			if err != nil {
				e.log.Error("job worker claim failed", "error", err)
				break
			}
			if !ran {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Stop marks the engine as shutting down and cancels running jobs.
func (e *Engine) Stop() {
	e.mu.Lock()
	e.shutdown = true
	for _, cancel := range e.cancel {
		cancel()
	}
	e.mu.Unlock()
}

func (e *Engine) runNext(ctx context.Context) (bool, error) {
	job, err := e.store.ClaimNextJob(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	e.runJob(ctx, job)
	return true, nil
}

func (e *Engine) runJob(parent context.Context, job *store.Job) {
	handler, ok := e.handlers[job.Type]
	if !ok {
		_ = e.store.FailJob(context.Background(), job.ID, "no handler registered for job type "+job.Type)
		return
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	e.mu.Lock()
	e.cancel[job.ID] = cancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.cancel, job.ID)
		e.mu.Unlock()
	}()

	e.log.Info("job started", "job", job.ID, "type", job.Type, "attempt", job.Attempts)
	ex := &Execution{Engine: e, Job: job, Store: e.store, Log: e.log}
	var result any
	var runErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				runErr = fmt.Errorf("panic in job %s: %v", job.Type, r)
			}
		}()
		result, runErr = handler(ctx, ex)
	}()
	if runErr == nil {
		raw, _ := json.Marshal(result)
		if err := e.store.CompleteJob(context.Background(), job.ID, string(raw)); err != nil {
			e.log.Error("complete job", "job", job.ID, "error", err)
		}
		e.log.Info("job succeeded", "job", job.ID, "type", job.Type)
		return
	}
	if canceled, _ := e.store.IsJobCanceled(context.Background(), job.ID); canceled || errors.Is(runErr, context.Canceled) {
		_ = e.store.MarkJobCanceled(context.Background(), job.ID)
		e.log.Info("job canceled", "job", job.ID, "type", job.Type)
		return
	}
	e.mu.Lock()
	shuttingDown := e.shutdown
	e.mu.Unlock()
	if shuttingDown || errors.Is(runErr, context.Canceled) {
		_ = e.store.RequeueInterrupted(context.Background(), job.ID, "manager shutting down; job will resume")
		e.log.Warn("job interrupted by shutdown", "job", job.ID, "type", job.Type)
		return
	}
	var retryErr *RetryableError
	if errors.As(runErr, &retryErr) && job.Attempts < job.MaxAttempts {
		delay := retryErr.RetryAfter
		if delay <= 0 {
			delay = backoff(job.Attempts)
		}
		if err := e.store.RequeueJob(context.Background(), job.ID, truncate(runErr.Error(), 2000), delay); err != nil {
			e.log.Error("requeue job", "job", job.ID, "error", err)
		}
		e.log.Warn("job scheduled for retry", "job", job.ID, "type", job.Type, "attempt", job.Attempts, "delay", delay, "error", runErr)
		return
	}
	if err := e.store.FailJob(context.Background(), job.ID, truncate(runErr.Error(), 4000)); err != nil {
		e.log.Error("fail job", "job", job.ID, "error", err)
	}
	e.log.Error("job failed", "job", job.ID, "type", job.Type, "error", runErr)
}

func backoff(attempt int) time.Duration {
	d := 15 * time.Second
	for i := 1; i < attempt && d < 10*time.Minute; i++ {
		d *= 2
	}
	if d > 10*time.Minute {
		d = 10 * time.Minute
	}
	return d
}

// Cancel requests cancellation and interrupts a running handler.
func (e *Engine) Cancel(ctx context.Context, id string) error {
	if err := e.store.RequestCancel(ctx, id); err != nil {
		return err
	}
	e.mu.Lock()
	if c, ok := e.cancel[id]; ok {
		c()
	}
	e.mu.Unlock()
	return nil
}

// Retry resets a failed/canceled job.
func (e *Engine) Retry(ctx context.Context, id string) error {
	return e.store.RetryJob(ctx, id)
}

// Execution is the handler's view of a durable job.
type Execution struct {
	Engine *Engine
	Job    *store.Job
	Store  *store.Store
	Log    *slog.Logger
}

// Step runs fn once and persists success. A successful checkpoint is skipped on
// retries/recovery; failed or interrupted steps are retried.
func (ex *Execution) Step(ctx context.Context, name, detail string, fn func(context.Context) (string, error)) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if canceled, _ := ex.Store.IsJobCanceled(ctx, ex.Job.ID); canceled {
		return context.Canceled
	}
	if existing, err := ex.Store.GetJobStep(ctx, ex.Job.ID, name); err == nil {
		if existing.Status == "succeeded" || existing.Status == "skipped" {
			return nil
		}
	}
	if err := ex.Store.BeginJobStep(ctx, ex.Job.ID, name, detail); err != nil {
		return err
	}
	output, err := fn(ctx)
	if err != nil {
		_ = ex.Store.FailJobStep(context.Background(), ex.Job.ID, name, truncate(err.Error(), 2000))
		return err
	}
	if err := ex.Store.FinishJobStep(ctx, ex.Job.ID, name, output); err != nil {
		return err
	}
	ex.Log.Info("job step completed", "job", ex.Job.ID, "step", name)
	return nil
}

// Skip records an intentional skipped step.
func (ex *Execution) Skip(ctx context.Context, name, detail string) error {
	return ex.Store.SkipJobStep(ctx, ex.Job.ID, name, detail)
}

// StepOutput returns a completed checkpoint's output, if any.
func (ex *Execution) StepOutput(ctx context.Context, name string) string {
	step, err := ex.Store.GetJobStep(ctx, ex.Job.ID, name)
	if err != nil {
		return ""
	}
	return step.Output
}

// Progress persists 0..100 progress.
func (ex *Execution) Progress(ctx context.Context, pct int) {
	_ = ex.Store.UpdateJobProgress(ctx, ex.Job.ID, pct)
}

// Payload decodes the job payload into v.
func (ex *Execution) Payload(v any) error {
	if err := json.Unmarshal([]byte(ex.Job.Payload), v); err != nil {
		return fmt.Errorf("invalid job payload: %w", err)
	}
	return nil
}

// SetJob replaces the in-memory job snapshot.
func (ex *Execution) SetJob(j *store.Job) { ex.Job = j }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
