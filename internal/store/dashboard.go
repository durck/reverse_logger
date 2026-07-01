package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

const (
	DefaultDashboardEventLimit = 100
	MaxDashboardEventLimit     = 500
)

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
	Recent              []DashboardEvent          `json:"recent"`
}

type DashboardTotals struct {
	Total        int `json:"total"`
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

type DashboardEventQuery struct {
	Window            time.Duration
	Status            string
	CorrelationStatus string
	Transport         string
	Search            string
	Limit             int
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
			OR lower(coalesce(ie.host, '')) LIKE ?
		)`)
		for i := 0; i < 13; i++ {
			args = append(args, like)
		}
	}

	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT ee.id, ee.status, ee.correlation_status, ee.correlation_method, ee.reverse_ssh_id,
	ee.host_name, ee.user_name, ee.computer_name, ee.ip_raw, ee.ip_addr, ee.ip_port,
	ee.real_client_ip, ee.client_port, ee.transport, ee.public_key_fingerprint,
	ee.proxy_source_ip, ee.vps_name, ee.vps_public_ip, ee.vps_internal_ip,
	ee.forwarder_ip, ie.host, ee.version, ee.received_at, ee.ingress_received_at
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
	bucketExpr := "substr(received_at, 1, 13) || ':00:00Z'"
	step := time.Hour
	layout := "2006-01-02T15:00:00Z"
	if bounds.window > 48*time.Hour {
		bucketExpr = "substr(received_at, 1, 10) || 'T00:00:00Z'"
		step = 24 * time.Hour
		layout = "2006-01-02T00:00:00Z"
	}

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
	return timeline, nil
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
	var correlationMethod, reverseSSHID, hostName, userName, computerName sql.NullString
	var ipRaw, ipAddr, realClientIP, transport, publicKeyFingerprint sql.NullString
	var proxySourceIP, vpsName, vpsPublicIP, vpsInternalIP, forwarderIP, ingressHost sql.NullString
	var version, ingressReceivedAt sql.NullString
	var ipPort, clientPort sql.NullInt64
	if err := row.Scan(
		&event.ID,
		&event.Status,
		&event.CorrelationStatus,
		&correlationMethod,
		&reverseSSHID,
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
