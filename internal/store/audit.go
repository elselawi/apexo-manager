package store

import (
	"context"
	"strings"
	"time"
)

// CreateAudit appends an audit entry.
func (s *Store) CreateAudit(ctx context.Context, a *AuditEntry) error {
	if a.ID == "" {
		a.ID = NewID()
	}
	if a.CreatedAt == "" {
		a.CreatedAt = Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_logs(id,actor_type,actor_id,actor_name,action,target_type,target_id,detail,ip,request_id,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.ActorType, a.ActorID, a.ActorName, a.Action, a.TargetType, a.TargetID, a.Detail, a.IP, a.RequestID, a.CreatedAt)
	return err
}

// ListAudit returns a filtered page and total.
func (s *Store) ListAudit(ctx context.Context, action, actor, query string, limit, offset int) ([]AuditEntry, int, error) {
	where := []string{"1=1"}
	var args []any
	if action != "" {
		where = append(where, "action LIKE ?")
		args = append(args, action+"%")
	}
	if actor != "" {
		where = append(where, "(actor_name LIKE ? OR actor_id = ?)")
		args = append(args, "%"+actor+"%", actor)
	}
	if query != "" {
		where = append(where, "(detail LIKE ? OR target_id LIKE ? OR action LIKE ?)")
		q := "%" + strings.ReplaceAll(query, "%", "") + "%"
		args = append(args, q, q, q)
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_logs WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	qargs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT id,actor_type,actor_id,actor_name,action,target_type,target_id,detail,ip,request_id,created_at
		FROM audit_logs WHERE `+clause+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, qargs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var v AuditEntry
		if err := rows.Scan(&v.ID, &v.ActorType, &v.ActorID, &v.ActorName, &v.Action, &v.TargetType, &v.TargetID, &v.Detail, &v.IP, &v.RequestID, &v.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}

// RecentAudit returns the latest entries.
func (s *Store) RecentAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	out, _, err := s.ListAudit(ctx, "", "", "", limit, 0)
	return out, err
}

// DeleteOldAudit prunes audit history.
func (s *Store) DeleteOldAudit(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM audit_logs WHERE created_at < ?`, FormatTime(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// GetServerSettings returns the raw JSON settings document.
func (s *Store) GetServerSettings(ctx context.Context) (string, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT data FROM server_settings WHERE id=1`).Scan(&data)
	if err == nil {
		return data, nil
	}
	if notFound(err) == ErrNotFound {
		return "", nil
	}
	return "", err
}

// SetServerSettings stores the JSON settings document.
func (s *Store) SetServerSettings(ctx context.Context, data string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO server_settings(id,data,updated_at) VALUES(1,?,?)
		ON CONFLICT(id) DO UPDATE SET data=excluded.data, updated_at=excluded.updated_at`, data, Now())
	return err
}

// InsertServerMetrics stores a sample.
func (s *Store) InsertServerMetrics(ctx context.Context, m *ServerMetrics) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO server_metrics(captured_at,cpu_percent,mem_used,mem_total,disk_used,disk_total,load1,uptime_seconds)
		VALUES(?,?,?,?,?,?,?,?)`, m.CapturedAt, m.CPUPercent, m.MemUsed, m.MemTotal, m.DiskUsed, m.DiskTotal, m.Load1, m.UptimeSeconds)
	return err
}

// ListServerMetrics returns recent samples, oldest first.
func (s *Store) ListServerMetrics(ctx context.Context, limit int) ([]ServerMetrics, error) {
	if limit <= 0 || limit > 1000 {
		limit = 120
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,captured_at,cpu_percent,mem_used,mem_total,disk_used,disk_total,load1,uptime_seconds
		FROM (SELECT * FROM server_metrics ORDER BY captured_at DESC LIMIT ?) ORDER BY captured_at ASC`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerMetrics{}
	for rows.Next() {
		var m ServerMetrics
		if err := rows.Scan(&m.ID, &m.CapturedAt, &m.CPUPercent, &m.MemUsed, &m.MemTotal, &m.DiskUsed, &m.DiskTotal, &m.Load1, &m.UptimeSeconds); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// LatestServerMetrics returns the newest sample.
func (s *Store) LatestServerMetrics(ctx context.Context) (*ServerMetrics, error) {
	var m ServerMetrics
	err := s.db.QueryRowContext(ctx, `SELECT id,captured_at,cpu_percent,mem_used,mem_total,disk_used,disk_total,load1,uptime_seconds
		FROM server_metrics ORDER BY captured_at DESC LIMIT 1`).Scan(&m.ID, &m.CapturedAt, &m.CPUPercent, &m.MemUsed, &m.MemTotal, &m.DiskUsed, &m.DiskTotal, &m.Load1, &m.UptimeSeconds)
	return &m, notFound(err)
}

// InsertInstanceMetrics stores an instance sample.
func (s *Store) InsertInstanceMetrics(ctx context.Context, m *InstanceMetrics) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO instance_metrics(instance_id,captured_at,status,health,mem_bytes,disk_bytes,cpu_percent,port_listening,error)
		VALUES(?,?,?,?,?,?,?,?,?)`, m.InstanceID, m.CapturedAt, m.Status, m.Health, m.MemBytes, m.DiskBytes, m.CPUPercent, boolInt(m.PortListening), m.Error)
	return err
}

// LatestInstanceMetrics returns the newest sample for each instance.
func (s *Store) LatestInstanceMetrics(ctx context.Context) (map[string]InstanceMetrics, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.instance_id,m.captured_at,m.status,m.health,m.mem_bytes,m.disk_bytes,m.cpu_percent,m.port_listening,m.error
		FROM instance_metrics m JOIN (SELECT instance_id, MAX(id) AS mid FROM instance_metrics GROUP BY instance_id) x ON x.mid=m.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]InstanceMetrics{}
	for rows.Next() {
		var m InstanceMetrics
		var listening int
		if err := rows.Scan(&m.InstanceID, &m.CapturedAt, &m.Status, &m.Health, &m.MemBytes, &m.DiskBytes, &m.CPUPercent, &listening, &m.Error); err != nil {
			return nil, err
		}
		m.PortListening = listening != 0
		out[m.InstanceID] = m
	}
	return out, rows.Err()
}

// ListInstanceMetrics returns recent samples for an instance.
func (s *Store) ListInstanceMetrics(ctx context.Context, instanceID string, limit int) ([]InstanceMetrics, error) {
	if limit <= 0 || limit > 1000 {
		limit = 120
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,instance_id,captured_at,status,health,mem_bytes,disk_bytes,cpu_percent,port_listening,error
		FROM (SELECT * FROM instance_metrics WHERE instance_id=? ORDER BY captured_at DESC LIMIT ?) ORDER BY captured_at ASC`, instanceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InstanceMetrics{}
	for rows.Next() {
		var m InstanceMetrics
		var listening int
		if err := rows.Scan(&m.ID, &m.InstanceID, &m.CapturedAt, &m.Status, &m.Health, &m.MemBytes, &m.DiskBytes, &m.CPUPercent, &listening, &m.Error); err != nil {
			return nil, err
		}
		m.PortListening = listening != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

// PruneMetrics removes samples older than the cutoff.
func (s *Store) PruneMetrics(ctx context.Context, before time.Time) error {
	cutoff := FormatTime(before)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM server_metrics WHERE captured_at < ?`, cutoff); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM instance_metrics WHERE captured_at < ?`, cutoff)
	return err
}
