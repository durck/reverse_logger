package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	DefaultDashboardEventLimit          = 100
	MaxDashboardEventLimit              = 500
	DefaultDashboardActiveSessionMaxAge = time.Hour
)

type DashboardConfig struct {
	ActiveSessionMaxAge time.Duration
}

func DefaultDashboardConfig() DashboardConfig {
	return DashboardConfig{
		ActiveSessionMaxAge: DefaultDashboardActiveSessionMaxAge,
	}
}

type DashboardOverview struct {
	Window              string                    `json:"window"`
	Since               string                    `json:"since"`
	Until               string                    `json:"until"`
	Totals              DashboardTotals           `json:"totals"`
	ByStatus            []DashboardCount          `json:"by_status"`
	ByCorrelationStatus []DashboardCount          `json:"by_correlation_status"`
	ByCorrelationMethod []DashboardCount          `json:"by_correlation_method"`
	ByTransport         []DashboardCount          `json:"by_transport"`
	ByVPS               []DashboardCount          `json:"by_vps"`
	Timeline            []DashboardTimelineBucket `json:"timeline"`
	ActiveSessions      []DashboardEvent          `json:"active_sessions"`
	Recent              []DashboardEvent          `json:"recent"`
}

type DashboardTotals struct {
	Total        int `json:"total"`
	Active       int `json:"active"`
	Connected    int `json:"connected"`
	Disconnected int `json:"disconnected"`
	Matched      int `json:"matched"`
	Unmatched    int `json:"unmatched"`
	Ambiguous    int `json:"ambiguous"`
}

type DashboardCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type DashboardTimelineBucket struct {
	BucketStart  string `json:"bucket_start"`
	Total        int    `json:"total"`
	Active       int    `json:"active"`
	ActiveEnd    int    `json:"active_end"`
	Connected    int    `json:"connected"`
	Disconnected int    `json:"disconnected"`
	Matched      int    `json:"matched"`
	Unmatched    int    `json:"unmatched"`
	Ambiguous    int    `json:"ambiguous"`
}

type DashboardEvent struct {
	ID                   int64  `json:"id"`
	Status               string `json:"status"`
	CorrelationStatus    string `json:"correlation_status"`
	CorrelationMethod    string `json:"correlation_method,omitempty"`
	IngestSource         string `json:"ingest_source,omitempty"`
	IngestReason         string `json:"ingest_reason,omitempty"`
	Synthetic            bool   `json:"synthetic,omitempty"`
	ReverseSSHID         string `json:"reverse_ssh_id,omitempty"`
	HostName             string `json:"host_name,omitempty"`
	UserName             string `json:"user_name,omitempty"`
	ComputerName         string `json:"computer_name,omitempty"`
	IPRaw                string `json:"ip_raw,omitempty"`
	IPAddr               string `json:"ip_addr,omitempty"`
	IPPort               int    `json:"ip_port,omitempty"`
	RealClientIP         string `json:"real_client_ip,omitempty"`
	ClientPort           int    `json:"client_port,omitempty"`
	Transport            string `json:"transport,omitempty"`
	PublicKeyFingerprint string `json:"public_key_fingerprint,omitempty"`
	ProxySourceIP        string `json:"proxy_source_ip,omitempty"`
	VPSName              string `json:"vps_name,omitempty"`
	VPSPublicIP          string `json:"vps_public_ip,omitempty"`
	VPSInternalIP        string `json:"vps_internal_ip,omitempty"`
	ForwarderIP          string `json:"forwarder_ip,omitempty"`
	IngressHost          string `json:"ingress_host,omitempty"`
	Version              string `json:"version,omitempty"`
	ReceivedAt           string `json:"received_at"`
	IngressReceivedAt    string `json:"ingress_received_at,omitempty"`
}

type DashboardSystemEvent struct {
	ID            int64  `json:"id"`
	Kind          string `json:"kind"`
	Severity      string `json:"severity"`
	Reason        string `json:"reason"`
	Source        string `json:"source"`
	Unit          string `json:"unit,omitempty"`
	Message       string `json:"message"`
	ClientIP      string `json:"client_ip,omitempty"`
	ClientPort    int    `json:"client_port,omitempty"`
	RemoteAddr    string `json:"remote_addr,omitempty"`
	Transport     string `json:"transport,omitempty"`
	Host          string `json:"host,omitempty"`
	URI           string `json:"uri,omitempty"`
	Method        string `json:"method,omitempty"`
	VPSName       string `json:"vps_name,omitempty"`
	VPSPublicIP   string `json:"vps_public_ip,omitempty"`
	VPSInternalIP string `json:"vps_internal_ip,omitempty"`
	ForwarderIP   string `json:"forwarder_ip,omitempty"`
	Fingerprint   string `json:"fingerprint,omitempty"`
	ObservedAt    string `json:"observed_at"`
	ReceivedAt    string `json:"received_at"`
}

type DashboardEventQuery struct {
	Window            time.Duration
	Status            string
	CorrelationStatus string
	Transport         string
	Search            string
	Limit             int
}

type DashboardSystemEventQuery struct {
	Window   time.Duration
	Kind     string
	Severity string
	Search   string
	Limit    int
}

func (s *Store) DashboardOverview(ctx context.Context, window time.Duration) (DashboardOverview, error) {
	bounds := dashboardBounds(window)
	overview := DashboardOverview{
		Window: formatDashboardWindow(bounds.window),
		Since:  bounds.since,
		Until:  bounds.until,
	}

	totals, err := s.dashboardTotals(ctx, bounds)
	if err != nil {
		return DashboardOverview{}, err
	}
	overview.Totals = totals

	if overview.ByStatus, err = s.dashboardCounts(ctx, bounds, "status"); err != nil {
		return DashboardOverview{}, err
	}
	if overview.ByCorrelationStatus, err = s.dashboardCounts(ctx, bounds, "correlation_status"); err != nil {
		return DashboardOverview{}, err
	}
	if overview.ByCorrelationMethod, err = s.dashboardCounts(ctx, bounds, "correlation_method"); err != nil {
		return DashboardOverview{}, err
	}
	if overview.ByTransport, err = s.dashboardCounts(ctx, bounds, "transport"); err != nil {
		return DashboardOverview{}, err
	}
	if overview.ByVPS, err = s.dashboardCounts(ctx, bounds, "vps_name"); err != nil {
		return DashboardOverview{}, err
	}
	if overview.Timeline, err = s.dashboardTimeline(ctx, bounds); err != nil {
		return DashboardOverview{}, err
	}
	activeSince := dashboardActiveSince(bounds, s.dashboard.ActiveSessionMaxAge)
	if overview.ActiveSessions, err = s.dashboardActiveSessions(ctx, 100, activeSince); err != nil {
		return DashboardOverview{}, err
	}
	if overview.Totals.Active, err = s.dashboardActiveSessionCount(ctx, activeSince); err != nil {
		return DashboardOverview{}, err
	}

	overview.Recent, err = s.DashboardEvents(ctx, DashboardEventQuery{
		Window: bounds.window,
		Limit:  25,
	})
	if err != nil {
		return DashboardOverview{}, err
	}
	return overview, nil
}

func (s *Store) DashboardEvents(ctx context.Context, query DashboardEventQuery) ([]DashboardEvent, error) {
	bounds := dashboardBounds(query.Window)
	limit := normalizeDashboardLimit(query.Limit)

	args := []any{bounds.since, bounds.until}
	conditions := []string{"ee.received_at BETWEEN ? AND ?"}
	for column, value := range map[string]string{
		"ee.status":             strings.ToLower(strings.TrimSpace(query.Status)),
		"ee.correlation_status": strings.ToLower(strings.TrimSpace(query.CorrelationStatus)),
		"ee.transport":          strings.ToLower(strings.TrimSpace(query.Transport)),
	} {
		if value == "" {
			continue
		}
		conditions = append(conditions, column+" = ?")
		args = append(args, value)
	}

	if search := strings.ToLower(strings.TrimSpace(query.Search)); search != "" {
		like := "%" + search + "%"
		conditions = append(conditions, `(
			lower(coalesce(ee.reverse_ssh_id, '')) LIKE ?
			OR lower(coalesce(ee.host_name, '')) LIKE ?
			OR lower(coalesce(ee.user_name, '')) LIKE ?
			OR lower(coalesce(ee.computer_name, '')) LIKE ?
			OR lower(coalesce(ee.ip_raw, '')) LIKE ?
			OR lower(coalesce(ee.ip_addr, '')) LIKE ?
			OR lower(coalesce(ee.real_client_ip, '')) LIKE ?
			OR lower(coalesce(ee.proxy_source_ip, '')) LIKE ?
			OR lower(coalesce(ee.vps_name, '')) LIKE ?
			OR lower(coalesce(ee.vps_public_ip, '')) LIKE ?
			OR lower(coalesce(ee.vps_internal_ip, '')) LIKE ?
			OR lower(coalesce(ee.forwarder_ip, '')) LIKE ?
			OR lower(coalesce(ee.ingest_source, '')) LIKE ?
			OR lower(coalesce(ee.ingest_reason, '')) LIKE ?
			OR lower(coalesce(ie.host, '')) LIKE ?
		)`)
		for i := 0; i < 15; i++ {
			args = append(args, like)
		}
	}

	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT ee.id, ee.status, ee.correlation_status, ee.correlation_method, ee.reverse_ssh_id,
	ee.ingest_source, ee.ingest_reason, ee.synthetic, ee.host_name, ee.user_name,
	ee.computer_name, ee.ip_raw, ee.ip_addr, ee.ip_port, ee.real_client_ip,
	ee.client_port, ee.transport, ee.public_key_fingerprint, ee.proxy_source_ip,
	ee.vps_name, ee.vps_public_ip, ee.vps_internal_ip, ee.forwarder_ip, ie.host,
	ee.version, ee.received_at, ee.ingress_received_at
FROM enriched_events ee
LEFT JOIN ingress_events ie ON ie.event_hash = ee.ingress_event_hash
WHERE `+strings.Join(conditions, " AND ")+`
ORDER BY ee.received_at DESC, ee.id DESC
LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]DashboardEvent, 0)
	for rows.Next() {
		event, err := scanDashboardEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) DashboardSystemEvents(ctx context.Context, query DashboardSystemEventQuery) ([]DashboardSystemEvent, error) {
	bounds := dashboardBounds(query.Window)
	limit := normalizeDashboardLimit(query.Limit)

	args := []any{bounds.since, bounds.until}
	conditions := []string{"received_at BETWEEN ? AND ?"}
	if kind := strings.ToLower(strings.TrimSpace(query.Kind)); kind != "" {
		conditions = append(conditions, "kind = ?")
		args = append(args, kind)
	}
	if severity := strings.ToLower(strings.TrimSpace(query.Severity)); severity == "not_info" || severity == "non_info" {
		conditions = append(conditions, "severity <> ?")
		args = append(args, "info")
	} else if severity != "" {
		conditions = append(conditions, "severity = ?")
		args = append(args, severity)
	}
	if search := strings.ToLower(strings.TrimSpace(query.Search)); search != "" {
		like := "%" + search + "%"
		conditions = append(conditions, `(
			lower(coalesce(source, '')) LIKE ?
			OR lower(coalesce(unit, '')) LIKE ?
			OR lower(coalesce(reason, '')) LIKE ?
			OR lower(coalesce(message, '')) LIKE ?
			OR lower(coalesce(client_ip, '')) LIKE ?
			OR lower(coalesce(remote_addr, '')) LIKE ?
			OR lower(coalesce(transport, '')) LIKE ?
			OR lower(coalesce(host, '')) LIKE ?
			OR lower(coalesce(uri, '')) LIKE ?
			OR lower(coalesce(vps_name, '')) LIKE ?
			OR lower(coalesce(vps_public_ip, '')) LIKE ?
			OR lower(coalesce(vps_internal_ip, '')) LIKE ?
			OR lower(coalesce(forwarder_ip, '')) LIKE ?
			OR lower(coalesce(fingerprint, '')) LIKE ?
		)`)
		for i := 0; i < 14; i++ {
			args = append(args, like)
		}
	}

	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
WITH system_events AS (
	SELECT id,
		'ingress' AS kind,
		'info' AS severity,
		'ingress' AS reason,
		'nginx-ingress' AS source,
		'' AS unit,
		trim(coalesce(method, '') || ' ' || coalesce(uri, '')) AS message,
		client_ip,
		client_port,
		CASE
			WHEN coalesce(client_ip, '') <> '' AND coalesce(client_port, 0) > 0 THEN client_ip || ':' || client_port
			ELSE coalesce(client_ip, '')
		END AS remote_addr,
		transport,
		host,
		uri,
		method,
		vps_name,
		vps_public_ip,
		vps_internal_ip,
		forwarder_ip,
		'' AS fingerprint,
		nginx_received_at AS observed_at,
		coalesce(nullif(forwarded_at, ''), nginx_received_at) AS received_at
	FROM ingress_events
	UNION ALL
	SELECT id,
		'reverse_ssh_error' AS kind,
		severity,
		reason,
		source,
		unit,
		message,
		remote_ip AS client_ip,
		remote_port AS client_port,
		remote_addr,
		transport,
		host,
		'' AS uri,
		'' AS method,
		'' AS vps_name,
		'' AS vps_public_ip,
		'' AS vps_internal_ip,
		'' AS forwarder_ip,
		fingerprint,
		observed_at,
		received_at
	FROM reverse_ssh_errors
)
SELECT id, kind, severity, reason, source, unit, message, client_ip, client_port,
	remote_addr, transport, host, uri, method, vps_name, vps_public_ip,
	vps_internal_ip, forwarder_ip, fingerprint, observed_at, received_at
FROM system_events
WHERE `+strings.Join(conditions, " AND ")+`
ORDER BY received_at DESC, id DESC
LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]DashboardSystemEvent, 0)
	for rows.Next() {
		event, err := scanDashboardSystemEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

type dashboardTimeBounds struct {
	window time.Duration
	since  string
	until  string
}

func dashboardBounds(window time.Duration) dashboardTimeBounds {
	if window <= 0 {
		window = 24 * time.Hour
	}
	until := time.Now().UTC()
	since := until.Add(-window)
	return dashboardTimeBounds{
		window: window,
		since:  since.Format(time.RFC3339Nano),
		until:  until.Format(time.RFC3339Nano),
	}
}

func formatDashboardWindow(window time.Duration) string {
	switch window {
	case 24 * time.Hour:
		return "24h"
	case 7 * 24 * time.Hour:
		return "7d"
	case 30 * 24 * time.Hour:
		return "30d"
	default:
		return window.String()
	}
}

func dashboardActiveSince(bounds dashboardTimeBounds, maxAge time.Duration) string {
	if maxAge <= 0 {
		return ""
	}
	until, err := time.Parse(time.RFC3339Nano, bounds.until)
	if err != nil {
		until = time.Now().UTC()
	}
	return until.UTC().Add(-maxAge).Format(time.RFC3339Nano)
}

func normalizeDashboardLimit(limit int) int {
	if limit <= 0 {
		return DefaultDashboardEventLimit
	}
	if limit > MaxDashboardEventLimit {
		return MaxDashboardEventLimit
	}
	return limit
}

func (s *Store) dashboardTotals(ctx context.Context, bounds dashboardTimeBounds) (DashboardTotals, error) {
	var totals DashboardTotals
	err := s.db.QueryRowContext(ctx, `
SELECT count(*),
	coalesce(sum(CASE WHEN status = 'connected' THEN 1 ELSE 0 END), 0),
	coalesce(sum(CASE WHEN status = 'disconnected' THEN 1 ELSE 0 END), 0),
	coalesce(sum(CASE WHEN correlation_status = 'matched' THEN 1 ELSE 0 END), 0),
	coalesce(sum(CASE WHEN correlation_status = 'unmatched' THEN 1 ELSE 0 END), 0),
	coalesce(sum(CASE WHEN correlation_status = 'ambiguous' THEN 1 ELSE 0 END), 0)
FROM enriched_events
WHERE received_at BETWEEN ? AND ?`, bounds.since, bounds.until).Scan(
		&totals.Total,
		&totals.Connected,
		&totals.Disconnected,
		&totals.Matched,
		&totals.Unmatched,
		&totals.Ambiguous,
	)
	if err == sql.ErrNoRows {
		return DashboardTotals{}, nil
	}
	return totals, err
}

func (s *Store) dashboardCounts(ctx context.Context, bounds dashboardTimeBounds, column string) ([]DashboardCount, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT coalesce(nullif(`+column+`, ''), 'unknown') AS name, count(*) AS total
FROM enriched_events
WHERE received_at BETWEEN ? AND ?
GROUP BY name
ORDER BY total DESC, name ASC
LIMIT 20`, bounds.since, bounds.until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make([]DashboardCount, 0)
	for rows.Next() {
		var count DashboardCount
		if err := rows.Scan(&count.Name, &count.Count); err != nil {
			return nil, err
		}
		counts = append(counts, count)
	}
	return counts, rows.Err()
}

func (s *Store) dashboardTimeline(ctx context.Context, bounds dashboardTimeBounds) ([]DashboardTimelineBucket, error) {
	bucketExpr, step, layout := dashboardTimelineResolution(bounds.window)

	rows, err := s.db.QueryContext(ctx, `
SELECT `+bucketExpr+` AS bucket_start,
	count(*) AS total,
	coalesce(sum(CASE WHEN status = 'connected' THEN 1 ELSE 0 END), 0) AS connected,
	coalesce(sum(CASE WHEN status = 'disconnected' THEN 1 ELSE 0 END), 0) AS disconnected,
	coalesce(sum(CASE WHEN correlation_status = 'matched' THEN 1 ELSE 0 END), 0) AS matched,
	coalesce(sum(CASE WHEN correlation_status = 'unmatched' THEN 1 ELSE 0 END), 0) AS unmatched,
	coalesce(sum(CASE WHEN correlation_status = 'ambiguous' THEN 1 ELSE 0 END), 0) AS ambiguous
FROM enriched_events
WHERE received_at BETWEEN ? AND ?
GROUP BY bucket_start
ORDER BY bucket_start ASC`, bounds.since, bounds.until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byBucket := map[string]DashboardTimelineBucket{}
	for rows.Next() {
		var bucket DashboardTimelineBucket
		if err := rows.Scan(
			&bucket.BucketStart,
			&bucket.Total,
			&bucket.Connected,
			&bucket.Disconnected,
			&bucket.Matched,
			&bucket.Unmatched,
			&bucket.Ambiguous,
		); err != nil {
			return nil, err
		}
		byBucket[bucket.BucketStart] = bucket
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	since, err := time.Parse(time.RFC3339Nano, bounds.since)
	if err != nil {
		return nil, err
	}
	until, err := time.Parse(time.RFC3339Nano, bounds.until)
	if err != nil {
		return nil, err
	}
	since = truncateDashboardBucket(since, step)
	until = truncateDashboardBucket(until, step)

	timeline := make([]DashboardTimelineBucket, 0, int(until.Sub(since)/step)+1)
	for cursor := since; !cursor.After(until); cursor = cursor.Add(step) {
		key := cursor.Format(layout)
		bucket, ok := byBucket[key]
		if !ok {
			bucket = DashboardTimelineBucket{BucketStart: key}
		}
		timeline = append(timeline, bucket)
	}
	if err := s.applyActiveTimeline(ctx, bounds, timeline, step, layout); err != nil {
		return nil, err
	}
	return timeline, nil
}

func dashboardTimelineResolution(window time.Duration) (string, time.Duration, string) {
	if window > 14*24*time.Hour {
		return dashboardHourBucketExpr(6), 6 * time.Hour, "2006-01-02T15:00:00Z"
	}
	if window > 48*time.Hour {
		return dashboardHourBucketExpr(3), 3 * time.Hour, "2006-01-02T15:00:00Z"
	}
	return "substr(received_at, 1, 13) || ':00:00Z'", time.Hour, "2006-01-02T15:00:00Z"
}

func dashboardHourBucketExpr(hours int) string {
	return fmt.Sprintf(
		"substr(received_at, 1, 11) || printf('%%02d', (CAST(substr(received_at, 12, 2) AS INTEGER) / %[1]d) * %[1]d) || ':00:00Z'",
		hours,
	)
}

func (s *Store) dashboardActiveSessions(ctx context.Context, limit int, activeSince string) ([]DashboardEvent, error) {
	limit = normalizeDashboardLimit(limit)
	snapshotID, ok, err := s.latestFreshSessionSnapshotID(ctx, activeSince)
	if err != nil {
		return nil, err
	}
	if ok {
		return s.dashboardActiveSessionsFromSnapshot(ctx, snapshotID, limit)
	}

	args := make([]any, 0, 2)
	activeFilter := ""
	if strings.TrimSpace(activeSince) != "" {
		activeFilter = "  AND ee.received_at >= ?\n"
		args = append(args, activeSince)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
WITH latest AS (
	SELECT max(id) AS id
	FROM enriched_events
	WHERE reverse_ssh_id <> ''
	  AND lower(coalesce(ingest_source, '')) <> 'reconciler'
	GROUP BY reverse_ssh_id
)
SELECT ee.id, ee.status, ee.correlation_status, ee.correlation_method, ee.reverse_ssh_id,
	ee.ingest_source, ee.ingest_reason, ee.synthetic, ee.host_name, ee.user_name,
	ee.computer_name, ee.ip_raw, ee.ip_addr, ee.ip_port, ee.real_client_ip,
	ee.client_port, ee.transport, ee.public_key_fingerprint, ee.proxy_source_ip,
	ee.vps_name, ee.vps_public_ip, ee.vps_internal_ip, ee.forwarder_ip, ie.host,
	ee.version, ee.received_at, ee.ingress_received_at
FROM enriched_events ee
JOIN latest ON latest.id = ee.id
LEFT JOIN ingress_events ie ON ie.event_hash = ee.ingress_event_hash
WHERE ee.status = 'connected'
`+activeFilter+`
ORDER BY ee.received_at DESC, ee.id DESC
LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]DashboardEvent, 0)
	for rows.Next() {
		event, err := scanDashboardEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) dashboardActiveSessionCount(ctx context.Context, activeSince string) (int, error) {
	snapshotID, ok, err := s.latestFreshSessionSnapshotID(ctx, activeSince)
	if err != nil {
		return 0, err
	}
	if ok {
		return s.dashboardActiveSessionCountFromSnapshot(ctx, snapshotID)
	}

	var count int
	args := make([]any, 0, 1)
	activeFilter := ""
	if strings.TrimSpace(activeSince) != "" {
		activeFilter = "  AND ee.received_at >= ?\n"
		args = append(args, activeSince)
	}
	err = s.db.QueryRowContext(ctx, `
WITH latest AS (
	SELECT max(id) AS id
	FROM enriched_events
	WHERE reverse_ssh_id <> ''
	  AND lower(coalesce(ingest_source, '')) <> 'reconciler'
	GROUP BY reverse_ssh_id
)
SELECT count(*)
FROM enriched_events ee
JOIN latest ON latest.id = ee.id
WHERE ee.status = 'connected'
`+activeFilter, args...).Scan(&count)
	return count, err
}

func (s *Store) latestFreshSessionSnapshotID(ctx context.Context, activeSince string) (int64, bool, error) {
	args := make([]any, 0, 1)
	filter := ""
	if strings.TrimSpace(activeSince) != "" {
		filter = "WHERE checked_at >= ?"
		args = append(args, activeSince)
	}
	query := `
SELECT id
FROM session_snapshots
` + filter + `
ORDER BY checked_at DESC, id DESC
LIMIT 1`
	var snapshotID int64
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&snapshotID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return snapshotID, true, nil
}

func (s *Store) dashboardActiveSessionsFromSnapshot(ctx context.Context, snapshotID int64, limit int) ([]DashboardEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
WITH latest AS (
	SELECT max(id) AS id
	FROM enriched_events
	WHERE reverse_ssh_id <> ''
	  AND lower(coalesce(ingest_source, '')) <> 'reconciler'
	GROUP BY reverse_ssh_id
)
SELECT ee.id, 'connected' AS status, ee.correlation_status, ee.correlation_method, ee.reverse_ssh_id,
	ee.ingest_source, ee.ingest_reason, ee.synthetic, ee.host_name, ee.user_name,
	ee.computer_name, ee.ip_raw, ee.ip_addr, ee.ip_port, ee.real_client_ip,
	ee.client_port, ee.transport, ee.public_key_fingerprint, ee.proxy_source_ip,
	ee.vps_name, ee.vps_public_ip, ee.vps_internal_ip, ee.forwarder_ip, ie.host,
	ee.version, ee.received_at, ee.ingress_received_at
FROM session_snapshot_live_ids live
JOIN latest ON latest.id = (
	SELECT max(id)
	FROM enriched_events candidate
	WHERE candidate.reverse_ssh_id = live.reverse_ssh_id
	  AND lower(coalesce(candidate.ingest_source, '')) <> 'reconciler'
)
JOIN enriched_events ee ON ee.id = latest.id
LEFT JOIN ingress_events ie ON ie.event_hash = ee.ingress_event_hash
WHERE live.snapshot_id = ?
ORDER BY ee.received_at DESC, ee.id DESC
LIMIT ?`, snapshotID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]DashboardEvent, 0)
	for rows.Next() {
		event, err := scanDashboardEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) dashboardActiveSessionCountFromSnapshot(ctx context.Context, snapshotID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
SELECT count(*)
FROM session_snapshot_live_ids live
WHERE live.snapshot_id = ?
  AND EXISTS (
	SELECT 1
	FROM enriched_events ee
	WHERE ee.reverse_ssh_id = live.reverse_ssh_id
	  AND lower(coalesce(ee.ingest_source, '')) <> 'reconciler'
  )`, snapshotID).Scan(&count)
	return count, err
}

func (s *Store) applyActiveTimeline(ctx context.Context, bounds dashboardTimeBounds, timeline []DashboardTimelineBucket, step time.Duration, layout string) error {
	if len(timeline) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT reverse_ssh_id, status, received_at
FROM enriched_events
WHERE reverse_ssh_id <> ''
  AND lower(coalesce(ingest_source, '')) <> 'reconciler'
  AND received_at <= ?
ORDER BY received_at ASC, id ASC`, bounds.until)
	if err != nil {
		return err
	}
	defer rows.Close()

	stateEvents := make([]dashboardSessionStateEvent, 0)
	for rows.Next() {
		var event dashboardSessionStateEvent
		var receivedAt string
		if err := rows.Scan(&event.reverseSSHID, &event.status, &receivedAt); err != nil {
			return err
		}
		parsed, err := time.Parse(time.RFC3339Nano, receivedAt)
		if err != nil {
			continue
		}
		event.receivedAt = parsed.UTC()
		stateEvents = append(stateEvents, event)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	until, err := time.Parse(time.RFC3339Nano, bounds.until)
	if err != nil {
		return err
	}
	active := map[string]time.Time{}
	maxAge := s.dashboard.ActiveSessionMaxAge
	eventIndex := 0
	timelineStart, err := time.Parse(layout, timeline[0].BucketStart)
	if err != nil {
		return err
	}
	for eventIndex < len(stateEvents) && stateEvents[eventIndex].receivedAt.Before(timelineStart) {
		applyDashboardSessionState(active, stateEvents[eventIndex], maxAge)
		eventIndex++
	}
	for i := range timeline {
		bucketStart, err := time.Parse(layout, timeline[i].BucketStart)
		if err != nil {
			continue
		}
		bucketEnd := bucketStart.Add(step)
		if bucketEnd.After(until) {
			bucketEnd = until
		}
		pruneExpiredDashboardSessions(active, bucketStart)
		peak := len(active)
		for eventIndex < len(stateEvents) && !stateEvents[eventIndex].receivedAt.After(bucketEnd) {
			event := stateEvents[eventIndex]
			pruneExpiredDashboardSessions(active, event.receivedAt)
			applyDashboardSessionState(active, event, maxAge)
			if current := len(active); current > peak {
				peak = current
			}
			eventIndex++
		}
		pruneExpiredDashboardSessions(active, bucketEnd)
		timeline[i].Active = peak
		timeline[i].ActiveEnd = len(active)
	}
	return nil
}

func applyDashboardSessionState(active map[string]time.Time, event dashboardSessionStateEvent, maxAge time.Duration) {
	if event.status == "connected" {
		if maxAge <= 0 {
			active[event.reverseSSHID] = time.Time{}
			return
		}
		active[event.reverseSSHID] = event.receivedAt.Add(maxAge)
		return
	}
	delete(active, event.reverseSSHID)
}

func pruneExpiredDashboardSessions(active map[string]time.Time, now time.Time) {
	for id, expiresAt := range active {
		if !expiresAt.IsZero() && !expiresAt.After(now) {
			delete(active, id)
		}
	}
}

type dashboardSessionStateEvent struct {
	reverseSSHID string
	status       string
	receivedAt   time.Time
}

func truncateDashboardBucket(value time.Time, step time.Duration) time.Time {
	value = value.UTC()
	if step >= 24*time.Hour {
		year, month, day := value.Date()
		return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	}
	return value.Truncate(step)
}

func scanDashboardEvent(row rowScanner) (DashboardEvent, error) {
	var event DashboardEvent
	var correlationMethod, ingestSource, ingestReason, reverseSSHID, hostName, userName, computerName sql.NullString
	var ipRaw, ipAddr, realClientIP, transport, publicKeyFingerprint sql.NullString
	var proxySourceIP, vpsName, vpsPublicIP, vpsInternalIP, forwarderIP, ingressHost sql.NullString
	var version, ingressReceivedAt sql.NullString
	var synthetic, ipPort, clientPort sql.NullInt64
	if err := row.Scan(
		&event.ID,
		&event.Status,
		&event.CorrelationStatus,
		&correlationMethod,
		&reverseSSHID,
		&ingestSource,
		&ingestReason,
		&synthetic,
		&hostName,
		&userName,
		&computerName,
		&ipRaw,
		&ipAddr,
		&ipPort,
		&realClientIP,
		&clientPort,
		&transport,
		&publicKeyFingerprint,
		&proxySourceIP,
		&vpsName,
		&vpsPublicIP,
		&vpsInternalIP,
		&forwarderIP,
		&ingressHost,
		&version,
		&event.ReceivedAt,
		&ingressReceivedAt,
	); err != nil {
		return DashboardEvent{}, err
	}
	event.CorrelationMethod = correlationMethod.String
	event.IngestSource = ingestSource.String
	event.IngestReason = ingestReason.String
	event.Synthetic = synthetic.Valid && synthetic.Int64 != 0
	event.ReverseSSHID = reverseSSHID.String
	event.HostName = hostName.String
	event.UserName = userName.String
	event.ComputerName = computerName.String
	event.IPRaw = ipRaw.String
	event.IPAddr = ipAddr.String
	if ipPort.Valid {
		event.IPPort = int(ipPort.Int64)
	}
	event.RealClientIP = realClientIP.String
	if clientPort.Valid {
		event.ClientPort = int(clientPort.Int64)
	}
	event.Transport = transport.String
	event.PublicKeyFingerprint = publicKeyFingerprint.String
	event.ProxySourceIP = proxySourceIP.String
	event.VPSName = vpsName.String
	event.VPSPublicIP = vpsPublicIP.String
	event.VPSInternalIP = vpsInternalIP.String
	event.ForwarderIP = forwarderIP.String
	event.IngressHost = ingressHost.String
	event.Version = version.String
	event.IngressReceivedAt = ingressReceivedAt.String
	return event, nil
}

func scanDashboardSystemEvent(row rowScanner) (DashboardSystemEvent, error) {
	var event DashboardSystemEvent
	var unit, message, clientIP, remoteAddr, transport, host, uri, method sql.NullString
	var vpsName, vpsPublicIP, vpsInternalIP, forwarderIP, fingerprint sql.NullString
	var clientPort sql.NullInt64
	if err := row.Scan(
		&event.ID,
		&event.Kind,
		&event.Severity,
		&event.Reason,
		&event.Source,
		&unit,
		&message,
		&clientIP,
		&clientPort,
		&remoteAddr,
		&transport,
		&host,
		&uri,
		&method,
		&vpsName,
		&vpsPublicIP,
		&vpsInternalIP,
		&forwarderIP,
		&fingerprint,
		&event.ObservedAt,
		&event.ReceivedAt,
	); err != nil {
		return DashboardSystemEvent{}, err
	}
	event.Unit = unit.String
	event.Message = message.String
	event.ClientIP = clientIP.String
	if clientPort.Valid {
		event.ClientPort = int(clientPort.Int64)
	}
	event.RemoteAddr = remoteAddr.String
	event.Transport = transport.String
	event.Host = host.String
	event.URI = uri.String
	event.Method = method.String
	event.VPSName = vpsName.String
	event.VPSPublicIP = vpsPublicIP.String
	event.VPSInternalIP = vpsInternalIP.String
	event.ForwarderIP = forwarderIP.String
	event.Fingerprint = fingerprint.String
	return event, nil
}
