// Package store persists pull events and daily aggregates (SQLite).
// Image layers are never stored. See .ai/01_DESIGN.md §5.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
	"github.com/alex_wuyh/easy-docker-proxy/internal/record"

	_ "modernc.org/sqlite"
)

// Store is a SQLite-backed event and aggregate store.
type Store struct {
	db                     *sql.DB
	eventRetentionDays     int
	aggregateRetentionDays int
}

// Open opens (or creates) the database from storage config.
// Only driver "sqlite" is supported in M2.
func Open(cfg config.StorageConfig) (*Store, error) {
	driver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	if driver == "" {
		driver = "sqlite"
	}
	if driver != "sqlite" {
		return nil, fmt.Errorf("storage driver %q not supported (only sqlite in M2)", cfg.Driver)
	}
	dsn := cfg.DSN
	if dsn == "" {
		dsn = "file:data/proxy.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	}
	if err := ensureDSNDir(dsn); err != nil {
		return nil, err
	}
	// modernc.org/sqlite registers as "sqlite".
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite writer safety
	db.SetConnMaxLifetime(0)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	evDays := cfg.EventRetentionDays
	if evDays <= 0 {
		evDays = 30
	}
	agDays := cfg.AggregateRetentionDays
	if agDays <= 0 {
		agDays = 365
	}
	return &Store{
		db:                     db,
		eventRetentionDays:     evDays,
		aggregateRetentionDays: agDays,
	}, nil
}

func ensureDSNDir(dsn string) error {
	// file:path?... or file:path
	path := dsn
	if strings.HasPrefix(path, "file:") {
		path = strings.TrimPrefix(path, "file:")
	}
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	// Skip pure memory DSNs.
	if path == "" || path == ":memory:" || strings.HasPrefix(path, ":memory:") {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func migrate(db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS pull_events (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  ts            INTEGER NOT NULL,
  client_ip     TEXT    NOT NULL,
  registry      TEXT    NOT NULL,
  host          TEXT,
  event_type    TEXT    NOT NULL,
  repository    TEXT    NOT NULL,
  reference     TEXT,
  method        TEXT,
  status        INTEGER,
  bytes         INTEGER DEFAULT 0,
  duration_ms   INTEGER,
  user_agent    TEXT,
  error         TEXT
);
CREATE INDEX IF NOT EXISTS idx_events_ts ON pull_events(ts);
CREATE INDEX IF NOT EXISTS idx_events_repo ON pull_events(repository, ts);
CREATE INDEX IF NOT EXISTS idx_events_ip ON pull_events(client_ip, ts);
CREATE INDEX IF NOT EXISTS idx_events_reg ON pull_events(registry, ts);

CREATE TABLE IF NOT EXISTS stats_daily (
  day           TEXT NOT NULL,
  registry      TEXT NOT NULL,
  repository    TEXT NOT NULL,
  pulls         INTEGER DEFAULT 0,
  bytes_total   INTEGER DEFAULT 0,
  errors        INTEGER DEFAULT 0,
  PRIMARY KEY (day, registry, repository)
);

CREATE TABLE IF NOT EXISTS stats_daily_client (
  day           TEXT NOT NULL,
  client_ip     TEXT NOT NULL,
  pulls         INTEGER DEFAULT 0,
  bytes_total   INTEGER DEFAULT 0,
  PRIMARY KEY (day, client_ip)
);

CREATE TABLE IF NOT EXISTS web_users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  username      TEXT    NOT NULL UNIQUE,
  password_hash TEXT    NOT NULL,
  role          TEXT    NOT NULL DEFAULT 'admin',
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS web_sessions (
  token         TEXT PRIMARY KEY,
  user_id       INTEGER NOT NULL,
  expires_at    INTEGER NOT NULL,
  created_at    INTEGER NOT NULL,
  FOREIGN KEY(user_id) REFERENCES web_users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON web_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_exp ON web_sessions(expires_at);
`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// WriteBatch inserts events and upserts daily aggregates in one transaction.
func (s *Store) WriteBatch(ctx context.Context, events []record.Event) error {
	if s == nil || len(events) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	ins, err := tx.PrepareContext(ctx, `
INSERT INTO pull_events (
  ts, client_ip, registry, host, event_type, repository, reference,
  method, status, bytes, duration_ms, user_agent, error
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()

	upsDaily, err := tx.PrepareContext(ctx, `
INSERT INTO stats_daily (day, registry, repository, pulls, bytes_total, errors)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(day, registry, repository) DO UPDATE SET
  pulls = pulls + excluded.pulls,
  bytes_total = bytes_total + excluded.bytes_total,
  errors = errors + excluded.errors`)
	if err != nil {
		return err
	}
	defer upsDaily.Close()

	upsClient, err := tx.PrepareContext(ctx, `
INSERT INTO stats_daily_client (day, client_ip, pulls, bytes_total)
VALUES (?, ?, ?, ?)
ON CONFLICT(day, client_ip) DO UPDATE SET
  pulls = pulls + excluded.pulls,
  bytes_total = bytes_total + excluded.bytes_total`)
	if err != nil {
		return err
	}
	defer upsClient.Close()

	for _, e := range events {
		ts := e.TS
		if ts.IsZero() {
			ts = time.Now().UTC()
		} else {
			ts = ts.UTC()
		}
		tsUnix := ts.Unix()
		day := ts.Format("2006-01-02")
		repo := e.Repository
		if repo == "" {
			repo = "-"
		}
		ip := e.ClientIP
		if ip == "" {
			ip = "unknown"
		}

		if _, err := ins.ExecContext(ctx,
			tsUnix, ip, e.Registry, e.Host, string(e.EventType), repo, e.Reference,
			e.Method, e.Status, e.Bytes, e.DurationMS, e.UserAgent, e.Error,
		); err != nil {
			return fmt.Errorf("insert event: %w", err)
		}

		var pulls, errors int64
		if e.IsPull() {
			pulls = 1
		}
		if e.IsError() {
			errors = 1
		}
		if _, err := upsDaily.ExecContext(ctx, day, e.Registry, repo, pulls, e.Bytes, errors); err != nil {
			return fmt.Errorf("upsert stats_daily: %w", err)
		}
		if _, err := upsClient.ExecContext(ctx, day, ip, pulls, e.Bytes); err != nil {
			return fmt.Errorf("upsert stats_daily_client: %w", err)
		}
	}
	return tx.Commit()
}

// PurgeExpired deletes events/aggregates past retention. Safe to call periodically.
func (s *Store) PurgeExpired(ctx context.Context) (eventsDeleted, dailyDeleted, clientDeleted int64, err error) {
	if s == nil {
		return 0, 0, 0, nil
	}
	now := time.Now().UTC()
	eventCutoff := now.AddDate(0, 0, -s.eventRetentionDays).Unix()
	aggDay := now.AddDate(0, 0, -s.aggregateRetentionDays).Format("2006-01-02")

	res, err := s.db.ExecContext(ctx, `DELETE FROM pull_events WHERE ts < ?`, eventCutoff)
	if err != nil {
		return 0, 0, 0, err
	}
	eventsDeleted, _ = res.RowsAffected()

	res, err = s.db.ExecContext(ctx, `DELETE FROM stats_daily WHERE day < ?`, aggDay)
	if err != nil {
		return eventsDeleted, 0, 0, err
	}
	dailyDeleted, _ = res.RowsAffected()

	res, err = s.db.ExecContext(ctx, `DELETE FROM stats_daily_client WHERE day < ?`, aggDay)
	if err != nil {
		return eventsDeleted, dailyDeleted, 0, err
	}
	clientDeleted, _ = res.RowsAffected()
	return eventsDeleted, dailyDeleted, clientDeleted, nil
}

// StartRetentionLoop runs PurgeExpired on interval until ctx is done.
func (s *Store) StartRetentionLoop(ctx context.Context, interval time.Duration) {
	if s == nil {
		return
	}
	if interval <= 0 {
		interval = time.Hour
	}
	go func() {
		// Initial purge shortly after start.
		t := time.NewTimer(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				ed, dd, cd, err := s.PurgeExpired(ctx)
				if err != nil {
					log.Printf("[store] retention purge: %v", err)
				} else if ed+dd+cd > 0 {
					log.Printf("[store] retention purge: events=%d daily=%d clients=%d", ed, dd, cd)
				}
				t.Reset(interval)
			}
		}
	}()
}

// CountEvents returns total rows in pull_events (tests / health).
func (s *Store) CountEvents(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pull_events`).Scan(&n)
	return n, err
}

// DailyStat is one row from stats_daily.
type DailyStat struct {
	Day        string
	Registry   string
	Repository string
	Pulls      int64
	BytesTotal int64
	Errors     int64
}

// QueryDaily returns stats_daily rows for day (YYYY-MM-DD), optional registry filter.
func (s *Store) QueryDaily(ctx context.Context, day, registry string) ([]DailyStat, error) {
	q := `SELECT day, registry, repository, pulls, bytes_total, errors FROM stats_daily WHERE day = ?`
	args := []any{day}
	if registry != "" {
		q += ` AND registry = ?`
		args = append(args, registry)
	}
	q += ` ORDER BY pulls DESC, bytes_total DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyStat
	for rows.Next() {
		var d DailyStat
		if err := rows.Scan(&d.Day, &d.Registry, &d.Repository, &d.Pulls, &d.BytesTotal, &d.Errors); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DB exposes the underlying *sql.DB for advanced queries (M3 stats API).
func (s *Store) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}
