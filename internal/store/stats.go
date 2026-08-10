package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Summary is the dashboard overview payload.
type Summary struct {
	Range           string          `json:"range"`
	TodayPulls      int64           `json:"today_pulls"`
	TodayBytes      int64           `json:"today_bytes"`
	TodayErrors     int64           `json:"today_errors"`
	RangePulls      int64           `json:"range_pulls"`
	RangeBytes      int64           `json:"range_bytes"`
	RangeErrors     int64           `json:"range_errors"`
	ErrorRate       float64         `json:"error_rate"` // range_errors / max(range_pulls+range_errors, 1)
	ActiveClients   int64           `json:"active_clients"`
	ByRegistry      []RegistryShare `json:"by_registry"`
	FromDay         string          `json:"from_day"`
	ToDay           string          `json:"to_day"`
}

// RegistryShare is pulls/bytes/errors share per upstream registry.
type RegistryShare struct {
	Registry   string  `json:"registry"`
	Pulls      int64   `json:"pulls"`
	BytesTotal int64   `json:"bytes_total"`
	Errors     int64   `json:"errors"`
	ErrorRate  float64 `json:"error_rate"`
	PullShare  float64 `json:"pull_share"`  // fraction of total pulls in range
	BytesShare float64 `json:"bytes_share"` // fraction of total bytes in range
}

// DayPoint is one day in a timeseries.
type DayPoint struct {
	Day        string `json:"day"`
	Pulls      int64  `json:"pulls"`
	BytesTotal int64  `json:"bytes_total"`
	Errors     int64  `json:"errors"`
}

// RepoRank is a top repository row.
type RepoRank struct {
	Registry   string `json:"registry"`
	Repository string `json:"repository"`
	Pulls      int64  `json:"pulls"`
	BytesTotal int64  `json:"bytes_total"`
	Errors     int64  `json:"errors"`
}

// ClientRank is a top client row.
type ClientRank struct {
	ClientIP   string `json:"client_ip"`
	Pulls      int64  `json:"pulls"`
	BytesTotal int64  `json:"bytes_total"`
}

// EventRow is a recent pull_events row for the UI.
type EventRow struct {
	ID         int64  `json:"id"`
	TS         int64  `json:"ts"`
	ClientIP   string `json:"client_ip"`
	Registry   string `json:"registry"`
	Host       string `json:"host"`
	EventType  string `json:"event_type"`
	Repository string `json:"repository"`
	Reference  string `json:"reference"`
	Method     string `json:"method"`
	Status     int    `json:"status"`
	Bytes      int64  `json:"bytes"`
	DurationMS int64  `json:"duration_ms"`
	UserAgent  string `json:"user_agent"`
	Error      string `json:"error"`
}

// ParseRange converts "7d"/"30d"/"1d" into inclusive day bounds (UTC).
func ParseRange(rangeStr string) (fromDay, toDay string, days int, err error) {
	s := strings.TrimSpace(strings.ToLower(rangeStr))
	if s == "" {
		s = "7d"
	}
	if !strings.HasSuffix(s, "d") {
		return "", "", 0, fmt.Errorf("invalid range %q (use e.g. 7d)", rangeStr)
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
	if err != nil || n < 1 || n > 366 {
		return "", "", 0, fmt.Errorf("invalid range %q", rangeStr)
	}
	now := time.Now().UTC()
	to := now
	from := now.AddDate(0, 0, -(n - 1))
	return from.Format("2006-01-02"), to.Format("2006-01-02"), n, nil
}

// Summary returns overview metrics for the given range (e.g. "7d").
func (s *Store) Summary(ctx context.Context, rangeStr string) (*Summary, error) {
	fromDay, toDay, _, err := ParseRange(rangeStr)
	if err != nil {
		return nil, err
	}
	today := time.Now().UTC().Format("2006-01-02")

	tp, tb, te, err := s.sumDaily(ctx, today, today)
	if err != nil {
		return nil, err
	}
	rp, rb, re, err := s.sumDaily(ctx, fromDay, toDay)
	if err != nil {
		return nil, err
	}

	var clients int64
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT client_ip) FROM stats_daily_client WHERE day >= ? AND day <= ?`,
		fromDay, toDay).Scan(&clients)
	if err != nil {
		return nil, err
	}

	byReg, err := s.registryShare(ctx, fromDay, toDay)
	if err != nil {
		return nil, err
	}

	denom := rp + re
	var rate float64
	if denom > 0 {
		rate = float64(re) / float64(denom)
	}

	return &Summary{
		Range:         rangeStr,
		TodayPulls:    tp,
		TodayBytes:    tb,
		TodayErrors:   te,
		RangePulls:    rp,
		RangeBytes:    rb,
		RangeErrors:   re,
		ErrorRate:     rate,
		ActiveClients: clients,
		ByRegistry:    byReg,
		FromDay:       fromDay,
		ToDay:         toDay,
	}, nil
}

func (s *Store) sumDaily(ctx context.Context, fromDay, toDay string) (pulls, bytes, errors int64, err error) {
	err = s.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(pulls),0), COALESCE(SUM(bytes_total),0), COALESCE(SUM(errors),0)
FROM stats_daily WHERE day >= ? AND day <= ?`, fromDay, toDay).Scan(&pulls, &bytes, &errors)
	return
}

func (s *Store) registryShare(ctx context.Context, fromDay, toDay string) ([]RegistryShare, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT registry, COALESCE(SUM(pulls),0), COALESCE(SUM(bytes_total),0), COALESCE(SUM(errors),0)
FROM stats_daily WHERE day >= ? AND day <= ?
GROUP BY registry ORDER BY SUM(pulls) DESC, SUM(bytes_total) DESC`, fromDay, toDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RegistryShare
	var totalPulls, totalBytes int64
	for rows.Next() {
		var r RegistryShare
		if err := rows.Scan(&r.Registry, &r.Pulls, &r.BytesTotal, &r.Errors); err != nil {
			return nil, err
		}
		denom := r.Pulls + r.Errors
		if denom > 0 {
			r.ErrorRate = float64(r.Errors) / float64(denom)
		}
		totalPulls += r.Pulls
		totalBytes += r.BytesTotal
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if totalPulls > 0 {
			out[i].PullShare = float64(out[i].Pulls) / float64(totalPulls)
		}
		if totalBytes > 0 {
			out[i].BytesShare = float64(out[i].BytesTotal) / float64(totalBytes)
		}
	}
	if out == nil {
		out = []RegistryShare{}
	}
	return out, nil
}

// Timeseries returns per-day aggregates for the range.
// If registry is non-empty, only that upstream is included.
func (s *Store) Timeseries(ctx context.Context, rangeStr, registry string) ([]DayPoint, error) {
	fromDay, toDay, days, err := ParseRange(rangeStr)
	if err != nil {
		return nil, err
	}
	registry = strings.TrimSpace(registry)
	var rows *sql.Rows
	if registry == "" {
		rows, err = s.db.QueryContext(ctx, `
SELECT day, COALESCE(SUM(pulls),0), COALESCE(SUM(bytes_total),0), COALESCE(SUM(errors),0)
FROM stats_daily WHERE day >= ? AND day <= ?
GROUP BY day ORDER BY day ASC`, fromDay, toDay)
	} else {
		rows, err = s.db.QueryContext(ctx, `
SELECT day, COALESCE(SUM(pulls),0), COALESCE(SUM(bytes_total),0), COALESCE(SUM(errors),0)
FROM stats_daily WHERE day >= ? AND day <= ? AND registry = ?
GROUP BY day ORDER BY day ASC`, fromDay, toDay, registry)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := make(map[string]DayPoint)
	for rows.Next() {
		var p DayPoint
		if err := rows.Scan(&p.Day, &p.Pulls, &p.BytesTotal, &p.Errors); err != nil {
			return nil, err
		}
		byDay[p.Day] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Fill missing days with zeros for stable charts.
	out := make([]DayPoint, 0, days)
	start, _ := time.ParseInLocation("2006-01-02", fromDay, time.UTC)
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		if p, ok := byDay[d]; ok {
			out = append(out, p)
		} else {
			out = append(out, DayPoint{Day: d})
		}
	}
	_ = toDay
	return out, nil
}

// TopRepos returns hottest repositories in the range.
// If registry is non-empty, only that upstream is included.
func (s *Store) TopRepos(ctx context.Context, rangeStr string, limit int, registry string) ([]RepoRank, error) {
	fromDay, toDay, _, err := ParseRange(rangeStr)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	registry = strings.TrimSpace(registry)
	var rows *sql.Rows
	if registry == "" {
		rows, err = s.db.QueryContext(ctx, `
SELECT registry, repository, COALESCE(SUM(pulls),0), COALESCE(SUM(bytes_total),0), COALESCE(SUM(errors),0)
FROM stats_daily WHERE day >= ? AND day <= ?
GROUP BY registry, repository
ORDER BY SUM(pulls) DESC, SUM(bytes_total) DESC
LIMIT ?`, fromDay, toDay, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
SELECT registry, repository, COALESCE(SUM(pulls),0), COALESCE(SUM(bytes_total),0), COALESCE(SUM(errors),0)
FROM stats_daily WHERE day >= ? AND day <= ? AND registry = ?
GROUP BY registry, repository
ORDER BY SUM(pulls) DESC, SUM(bytes_total) DESC
LIMIT ?`, fromDay, toDay, registry, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RepoRank
	for rows.Next() {
		var r RepoRank
		if err := rows.Scan(&r.Registry, &r.Repository, &r.Pulls, &r.BytesTotal, &r.Errors); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if out == nil {
		out = []RepoRank{}
	}
	return out, rows.Err()
}

// TopClients returns most active clients in the range.
func (s *Store) TopClients(ctx context.Context, rangeStr string, limit int) ([]ClientRank, error) {
	fromDay, toDay, _, err := ParseRange(rangeStr)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT client_ip, COALESCE(SUM(pulls),0), COALESCE(SUM(bytes_total),0)
FROM stats_daily_client WHERE day >= ? AND day <= ?
GROUP BY client_ip
ORDER BY SUM(pulls) DESC, SUM(bytes_total) DESC
LIMIT ?`, fromDay, toDay, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClientRank
	for rows.Next() {
		var c ClientRank
		if err := rows.Scan(&c.ClientIP, &c.Pulls, &c.BytesTotal); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if out == nil {
		out = []ClientRank{}
	}
	return out, rows.Err()
}

// RecentErrors returns latest failed pull_events.
func (s *Store) RecentErrors(ctx context.Context, limit int) ([]EventRow, error) {
	return s.queryEvents(ctx, limit, true)
}

// RecentEvents returns latest pull_events (all types).
func (s *Store) RecentEvents(ctx context.Context, limit int) ([]EventRow, error) {
	return s.queryEvents(ctx, limit, false)
}

func (s *Store) queryEvents(ctx context.Context, limit int, errorsOnly bool) ([]EventRow, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	q := `
SELECT id, ts, client_ip, registry, COALESCE(host,''), event_type, repository,
       COALESCE(reference,''), COALESCE(method,''), COALESCE(status,0),
       COALESCE(bytes,0), COALESCE(duration_ms,0), COALESCE(user_agent,''), COALESCE(error,'')
FROM pull_events`
	if errorsOnly {
		q += ` WHERE status >= 400 OR (error IS NOT NULL AND error != '')`
	}
	q += ` ORDER BY ts DESC, id DESC LIMIT ?`

	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		if err := rows.Scan(&e.ID, &e.TS, &e.ClientIP, &e.Registry, &e.Host, &e.EventType,
			&e.Repository, &e.Reference, &e.Method, &e.Status, &e.Bytes, &e.DurationMS,
			&e.UserAgent, &e.Error); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if out == nil {
		out = []EventRow{}
	}
	return out, rows.Err()
}
