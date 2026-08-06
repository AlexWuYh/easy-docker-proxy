package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ImageSummary is a repository aggregated across all pull events.
type ImageSummary struct {
	Registry      string `json:"registry"`
	Repository    string `json:"repository"`
	Pulls         int64  `json:"pulls"`
	BytesTotal    int64  `json:"bytes_total"`
	Errors        int64  `json:"errors"`
	TagCount      int64  `json:"tag_count"`
	LastPullTS    int64  `json:"last_pull_ts"`
	FirstPullTS   int64  `json:"first_pull_ts"`
	LastReference string `json:"last_reference"`
}

// ImageTagStat is one tag/digest under a repository.
type ImageTagStat struct {
	Reference  string `json:"reference"`
	Pulls      int64  `json:"pulls"`
	BytesTotal int64  `json:"bytes_total"`
	Errors     int64  `json:"errors"`
	LastPullTS int64  `json:"last_pull_ts"`
	FirstPullTS int64 `json:"first_pull_ts"`
}

// ListImages returns repositories merged by registry+name, ordered by last pull.
func (s *Store) ListImages(ctx context.Context, q string, limit, offset int) ([]ImageSummary, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	q = strings.TrimSpace(q)

	where := `WHERE event_type = 'manifest'`
	args := []any{}
	if q != "" {
		where += ` AND (repository LIKE ? OR registry LIKE ? OR COALESCE(reference,'') LIKE ?)`
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}

	var total int64
	countSQL := `SELECT COUNT(*) FROM (
SELECT registry, repository FROM pull_events ` + where + ` GROUP BY registry, repository)`
	if err := s.db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	sqlq := `
SELECT
  registry,
  repository,
  SUM(CASE WHEN status >= 200 AND status < 300 THEN 1 ELSE 0 END) AS pulls,
  COALESCE(SUM(bytes), 0) AS bytes_total,
  SUM(CASE WHEN status >= 400 OR (error IS NOT NULL AND error != '') THEN 1 ELSE 0 END) AS errors,
  COUNT(DISTINCT CASE WHEN reference IS NOT NULL AND reference != '' THEN reference END) AS tag_count,
  MAX(ts) AS last_ts,
  MIN(ts) AS first_ts,
  (SELECT pe2.reference FROM pull_events pe2
    WHERE pe2.registry = pull_events.registry AND pe2.repository = pull_events.repository
      AND pe2.event_type = 'manifest'
    ORDER BY pe2.ts DESC LIMIT 1) AS last_ref
FROM pull_events
` + where + `
GROUP BY registry, repository
ORDER BY last_ts DESC
LIMIT ? OFFSET ?`
	args2 := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, sqlq, args2...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []ImageSummary
	for rows.Next() {
		var im ImageSummary
		var lastRef *string
		if err := rows.Scan(&im.Registry, &im.Repository, &im.Pulls, &im.BytesTotal, &im.Errors,
			&im.TagCount, &im.LastPullTS, &im.FirstPullTS, &lastRef); err != nil {
			return nil, 0, err
		}
		if lastRef != nil {
			im.LastReference = *lastRef
		}
		out = append(out, im)
	}
	if out == nil {
		out = []ImageSummary{}
	}
	return out, total, rows.Err()
}

// ListImageTags returns tag/digest breakdown for one repository.
func (s *Store) ListImageTags(ctx context.Context, registry, repository string) ([]ImageTagStat, error) {
	registry = strings.TrimSpace(registry)
	repository = strings.TrimSpace(repository)
	if registry == "" || repository == "" {
		return nil, fmt.Errorf("registry and repository required")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT
  COALESCE(reference, '') AS reference,
  SUM(CASE WHEN status >= 200 AND status < 300 THEN 1 ELSE 0 END) AS pulls,
  COALESCE(SUM(bytes), 0) AS bytes_total,
  SUM(CASE WHEN status >= 400 OR (error IS NOT NULL AND error != '') THEN 1 ELSE 0 END) AS errors,
  MAX(ts) AS last_ts,
  MIN(ts) AS first_ts
FROM pull_events
WHERE event_type = 'manifest' AND registry = ? AND repository = ?
GROUP BY COALESCE(reference, '')
ORDER BY last_ts DESC`, registry, repository)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImageTagStat
	for rows.Next() {
		var t ImageTagStat
		if err := rows.Scan(&t.Reference, &t.Pulls, &t.BytesTotal, &t.Errors, &t.LastPullTS, &t.FirstPullTS); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if out == nil {
		out = []ImageTagStat{}
	}
	return out, rows.Err()
}

// ImageAnalytics returns timeseries for one repository within a range.
func (s *Store) ImageAnalytics(ctx context.Context, registry, repository, rangeStr string) ([]DayPoint, error) {
	fromDay, toDay, days, err := ParseRange(rangeStr)
	if err != nil {
		return nil, err
	}
	// Aggregate from stats_daily when available, else from events.
	rows, err := s.db.QueryContext(ctx, `
SELECT day, pulls, bytes_total, errors FROM stats_daily
WHERE registry = ? AND repository = ? AND day >= ? AND day <= ?
ORDER BY day ASC`, registry, repository, fromDay, toDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[string]DayPoint{}
	for rows.Next() {
		var p DayPoint
		if err := rows.Scan(&p.Day, &p.Pulls, &p.BytesTotal, &p.Errors); err != nil {
			return nil, err
		}
		byDay[p.Day] = p
	}
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
