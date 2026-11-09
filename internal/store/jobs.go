package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const jobColumns = `id, type, payload, status, priority, attempts, max_attempts, available_at, started_at, finished_at,
	last_error, progress, result, COALESCE(idempotency_key,''), created_by, canceled, recovered_count, created_at, updated_at`

func scanJob(sc interface{ Scan(...any) error }) (*Job, error) {
	var v Job
	var canceled int
	err := sc.Scan(&v.ID, &v.Type, &v.Payload, &v.Status, &v.Priority, &v.Attempts, &v.MaxAttempts,
		&v.AvailableAt, &v.StartedAt, &v.FinishedAt, &v.LastError, &v.Progress, &v.Result,
		&v.IdempotencyKey, &v.CreatedBy, &canceled, &v.RecoveredCount, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, err
	}
	v.Canceled = canceled != 0
	return &v, nil
}

// CreateJob inserts a queued job. If idempotencyKey already exists, the
// existing job is returned and created is false.
func (s *Store) CreateJob(ctx context.Context, j *Job) (*Job, bool, error) {
	if j.ID == "" {
		j.ID = NewID()
	}
	if j.CreatedAt == "" {
		j.CreatedAt = Now()
	}
	if j.Status == "" {
		j.Status = "queued"
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 4
	}
	if j.AvailableAt == "" {
		j.AvailableAt = Now()
	}
	j.UpdatedAt = Now()
	var key any
	if j.IdempotencyKey != "" {
		key = j.IdempotencyKey
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO jobs(id,type,payload,status,priority,attempts,max_attempts,available_at,
		started_at,finished_at,last_error,progress,result,idempotency_key,created_by,canceled,recovered_count,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.Type, j.Payload, j.Status, j.Priority, j.Attempts, j.MaxAttempts, j.AvailableAt,
		j.StartedAt, j.FinishedAt, j.LastError, j.Progress, j.Result, key, j.CreatedBy, boolInt(j.Canceled), j.RecoveredCount, j.CreatedAt, j.UpdatedAt)
	if err != nil {
		if j.IdempotencyKey != "" && strings.Contains(strings.ToLower(err.Error()), "unique") {
			existing, getErr := s.GetJobByKey(ctx, j.IdempotencyKey)
			if getErr == nil {
				return existing, false, nil
			}
		}
		return nil, false, err
	}
	return j, true, nil
}

// GetJob loads a job and its steps.
func (s *Store) GetJob(ctx context.Context, id string) (*Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, id))
	if err != nil {
		return nil, notFound(err)
	}
	steps, err := s.ListJobSteps(ctx, id)
	if err != nil {
		return nil, err
	}
	j.Steps = steps
	return j, nil
}

// GetJobByKey loads a job by idempotency key.
func (s *Store) GetJobByKey(ctx context.Context, key string) (*Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE idempotency_key=?`, key))
	return j, notFound(err)
}

// ClaimNextJob atomically moves the next runnable job to running.
func (s *Store) ClaimNextJob(ctx context.Context) (*Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM jobs WHERE status='queued' AND canceled=0 AND available_at<=?
		ORDER BY priority DESC, created_at ASC LIMIT 1`, Now()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	now := Now()
	res, err := tx.ExecContext(ctx, `UPDATE jobs SET status='running', attempts=attempts+1, started_at=?,
		finished_at='', updated_at=? WHERE id=? AND status='queued'`, now, now, id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetJob(ctx, id)
}

// RecoverRunningJobs requeues jobs interrupted by a crash. Running steps are
// reset to pending while successful checkpoints remain intact.
func (s *Store) RecoverRunningJobs(ctx context.Context) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM jobs WHERE status='running'`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	now := Now()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status='queued', available_at=?, recovered_count=recovered_count+1,
			last_error=?, updated_at=? WHERE id=?`, now, "manager restarted while job was running; recovered", now, id); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE job_steps SET status='pending', detail=?, finished_at='' WHERE job_id=? AND status='running'`,
			"interrupted; will retry", id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

// UpdateJobProgress sets progress (0..100).
func (s *Store) UpdateJobProgress(ctx context.Context, id string, progress int) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET progress=?, updated_at=? WHERE id=?`, progress, Now(), id)
	return err
}

// CompleteJob marks a job successful.
func (s *Store) CompleteJob(ctx context.Context, id, result string) error {
	now := Now()
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='succeeded', finished_at=?, progress=100, result=?, last_error='', updated_at=? WHERE id=?`,
		now, result, now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// FailJob marks a job failed permanently.
func (s *Store) FailJob(ctx context.Context, id, errText string) error {
	now := Now()
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='failed', finished_at=?, last_error=?, updated_at=? WHERE id=?`,
		now, errText, now, id)
	return err
}

// RequeueJob schedules a retry with a bounded delay.
func (s *Store) RequeueJob(ctx context.Context, id, errText string, delay time.Duration) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='queued', available_at=?, last_error=?, updated_at=? WHERE id=?`,
		FormatTime(now.Add(delay)), errText, FormatTime(now), id)
	return err
}

// RequeueInterrupted leaves a job runnable after a graceful shutdown. Running
// checkpoints are reset so the next worker attempt re-executes them.
func (s *Store) RequeueInterrupted(ctx context.Context, id, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := Now()
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status='queued', available_at=?, last_error=?, updated_at=? WHERE id=?`,
		now, reason, now, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE job_steps SET status='pending', detail=?, finished_at='' WHERE job_id=? AND status='running'`,
		"interrupted by shutdown; will retry", id); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkJobCanceled finalizes cancellation.
func (s *Store) MarkJobCanceled(ctx context.Context, id string) error {
	now := Now()
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='canceled', canceled=1, finished_at=?, updated_at=? WHERE id=?`, now, now, id)
	return err
}

// RequestCancel flags a job; running jobs are canceled by their worker.
func (s *Store) RequestCancel(ctx context.Context, id string) error {
	j, err := s.GetJob(ctx, id)
	if err != nil {
		return err
	}
	if j.Status == "succeeded" || j.Status == "failed" || j.Status == "canceled" {
		return fmt.Errorf("job is already %s", j.Status)
	}
	if j.Status == "queued" {
		_, err = s.db.ExecContext(ctx, `UPDATE jobs SET canceled=1, status='canceled', finished_at=?, updated_at=? WHERE id=?`, Now(), Now(), id)
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE jobs SET canceled=1, updated_at=? WHERE id=?`, Now(), id)
	return err
}

// IsJobCanceled reports the cancellation flag.
func (s *Store) IsJobCanceled(ctx context.Context, id string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT canceled FROM jobs WHERE id=?`, id).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return n != 0, err
}

// RetryJob resets a terminal job for another run. Successful step checkpoints
// are preserved, making retries safe for partially completed operations.
func (s *Store) RetryJob(ctx context.Context, id string) error {
	now := Now()
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='queued', canceled=0, attempts=0, available_at=?, finished_at='',
		last_error='', progress=0, updated_at=? WHERE id=? AND status IN ('failed','canceled')`, now, now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("only failed or canceled jobs can be retried")
	}
	_, err = s.db.ExecContext(ctx, `UPDATE job_steps SET status='pending', detail='manual retry', finished_at='' WHERE job_id=? AND status IN ('failed','running')`, id)
	return err
}

// ListJobs returns a filtered page and total count.
func (s *Store) ListJobs(ctx context.Context, status, jobType, query string, limit, offset int) ([]Job, int, error) {
	where := []string{"1=1"}
	var args []any
	if status != "" {
		where = append(where, "status=?")
		args = append(args, status)
	}
	if jobType != "" {
		where = append(where, "type=?")
		args = append(args, jobType)
	}
	if query != "" {
		where = append(where, "(id LIKE ? OR type LIKE ? OR last_error LIKE ? OR payload LIKE ?)")
		q := "%" + query + "%"
		args = append(args, q, q, q, q)
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	qargs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE `+clause+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, qargs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		v, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *v)
	}
	return out, total, rows.Err()
}

// ListActiveJobs returns queued/running jobs.
func (s *Store) ListActiveJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE status IN ('queued','running') ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		v, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// CountJobsByStatus returns counts for the overview.
func (s *Store) CountJobsByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM jobs GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// ListJobSteps returns ordered checkpoints.
func (s *Store) ListJobSteps(ctx context.Context, jobID string) ([]JobStep, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, job_id, name, status, detail, output, attempt, started_at, finished_at
		FROM job_steps WHERE job_id=? ORDER BY started_at, name`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobStep{}
	for rows.Next() {
		var v JobStep
		if err := rows.Scan(&v.ID, &v.JobID, &v.Name, &v.Status, &v.Detail, &v.Output, &v.Attempt, &v.StartedAt, &v.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetJobStep returns a checkpoint.
func (s *Store) GetJobStep(ctx context.Context, jobID, name string) (*JobStep, error) {
	var v JobStep
	err := s.db.QueryRowContext(ctx, `SELECT id, job_id, name, status, detail, output, attempt, started_at, finished_at
		FROM job_steps WHERE job_id=? AND name=?`, jobID, name).Scan(&v.ID, &v.JobID, &v.Name, &v.Status, &v.Detail, &v.Output, &v.Attempt, &v.StartedAt, &v.FinishedAt)
	return &v, notFound(err)
}

// BeginJobStep starts or retries a checkpoint.
func (s *Store) BeginJobStep(ctx context.Context, jobID, name, detail string) error {
	now := Now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO job_steps(id,job_id,name,status,detail,output,attempt,started_at,finished_at)
		VALUES(?,?,?,'running',?,'',1,?,'') ON CONFLICT(job_id,name) DO UPDATE SET
		status='running', detail=excluded.detail, attempt=job_steps.attempt+1, started_at=excluded.started_at, finished_at=''`,
		NewID(), jobID, name, detail, now)
	return err
}

// FinishJobStep records success and a human-readable output.
func (s *Store) FinishJobStep(ctx context.Context, jobID, name, output string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE job_steps SET status='succeeded', output=?, finished_at=? WHERE job_id=? AND name=?`,
		output, Now(), jobID, name)
	return err
}

// FailJobStep records failure detail.
func (s *Store) FailJobStep(ctx context.Context, jobID, name, detail string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE job_steps SET status='failed', detail=?, finished_at=? WHERE job_id=? AND name=?`,
		detail, Now(), jobID, name)
	return err
}

// SkipJobStep records an intentional no-op.
func (s *Store) SkipJobStep(ctx context.Context, jobID, name, detail string) error {
	now := Now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO job_steps(id,job_id,name,status,detail,output,attempt,started_at,finished_at)
		VALUES(?,?,?,'skipped',?,'',0,?,'') ON CONFLICT(job_id,name) DO UPDATE SET status='skipped', detail=excluded.detail,
		finished_at=excluded.finished_at`, NewID(), jobID, name, detail, now)
	return err
}

// DeleteFinishedJobs prunes old terminal jobs.
func (s *Store) DeleteFinishedJobs(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE finished_at<>'' AND finished_at < ?`, FormatTime(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
