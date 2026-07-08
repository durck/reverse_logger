package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/durck/reverse_logger/internal/edgehealth"
)

const DefaultEdgeHealthBootstrapGrace = 2 * time.Minute

type EdgeHealthSummary struct {
	Total    int `json:"total"`
	OK       int `json:"ok"`
	Degraded int `json:"degraded"`
	Down     int `json:"down"`
	Unknown  int `json:"unknown"`
}

type EdgeHealthOverview struct {
	GeneratedAt string                 `json:"generated_at"`
	Summary     EdgeHealthSummary      `json:"summary"`
	Nodes       []EdgeHealthNodeStatus `json:"nodes"`
}

type EdgeHealthNodeStatus struct {
	VPSName          string             `json:"vps_name"`
	VPSPublicIP      string             `json:"vps_public_ip,omitempty"`
	VPSInternalIP    string             `json:"vps_internal_ip,omitempty"`
	Status           string             `json:"status"`
	LastReportStatus string             `json:"last_report_status,omitempty"`
	FailedChecks     []string           `json:"failed_checks"`
	Checks           []edgehealth.Check `json:"checks,omitempty"`
	IntervalSeconds  int                `json:"interval_seconds"`
	MissedReports    int                `json:"missed_reports"`
	LastSeenAt       string             `json:"last_seen_at,omitempty"`
	LastOKAt         string             `json:"last_ok_at,omitempty"`
	CheckedAt        string             `json:"checked_at,omitempty"`
	StaleAfter       string             `json:"stale_after,omitempty"`
	BootstrapUntil   string             `json:"bootstrap_until,omitempty"`
	UpdatedAt        string             `json:"updated_at,omitempty"`

	storedStatus string
}

type EdgeHealthTransition struct {
	PreviousStatus string
	CurrentStatus  string
	Node           EdgeHealthNodeStatus
}

func (s *Store) initEdgeHealth() error {
	schema := `
CREATE TABLE IF NOT EXISTS edge_health_expected (
	vps_name TEXT PRIMARY KEY,
	vps_public_ip TEXT,
	vps_internal_ip TEXT,
	registered_at TEXT NOT NULL,
	bootstrap_until TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS edge_health_reports (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	vps_name TEXT NOT NULL,
	vps_public_ip TEXT,
	vps_internal_ip TEXT,
	status TEXT NOT NULL,
	failed_checks TEXT NOT NULL,
	checks_json TEXT NOT NULL,
	interval_seconds INTEGER NOT NULL,
	missed_reports INTEGER NOT NULL,
	checked_at TEXT NOT NULL,
	received_at TEXT NOT NULL,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS edge_health_state (
	vps_name TEXT PRIMARY KEY,
	vps_public_ip TEXT,
	vps_internal_ip TEXT,
	status TEXT NOT NULL,
	last_report_status TEXT,
	failed_checks TEXT NOT NULL,
	checks_json TEXT NOT NULL,
	interval_seconds INTEGER NOT NULL,
	missed_reports INTEGER NOT NULL,
	last_seen_at TEXT,
	last_ok_at TEXT,
	checked_at TEXT,
	updated_at TEXT NOT NULL,
	notified_status TEXT
);`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_edge_health_reports_vps_time ON edge_health_reports(vps_name, received_at)",
		"CREATE INDEX IF NOT EXISTS idx_edge_health_state_status ON edge_health_state(status)",
		"CREATE INDEX IF NOT EXISTS idx_edge_health_state_last_seen ON edge_health_state(last_seen_at)",
	}
	for _, stmt := range indexes {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) RegisterEdgeHealthExpected(nodes []edgehealth.ExpectedNode, bootstrapGrace time.Duration, now time.Time) error {
	if bootstrapGrace <= 0 {
		bootstrapGrace = DefaultEdgeHealthBootstrapGrace
	}
	now = now.UTC()
	registeredAt := now.Format(time.RFC3339Nano)
	bootstrapUntil := now.Add(bootstrapGrace).Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, node := range nodes {
		if strings.TrimSpace(node.VPSName) == "" {
			continue
		}
		_, err := tx.Exec(`
INSERT INTO edge_health_expected (
	vps_name, vps_public_ip, vps_internal_ip, registered_at, bootstrap_until, updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(vps_name) DO UPDATE SET
	vps_public_ip = excluded.vps_public_ip,
	vps_internal_ip = excluded.vps_internal_ip,
	updated_at = excluded.updated_at`,
			node.VPSName,
			node.VPSPublicIP,
			node.VPSInternalIP,
			registeredAt,
			bootstrapUntil,
			registeredAt,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RecordEdgeHealthReport(report edgehealth.Report, raw []byte, receivedAt time.Time) (EdgeHealthTransition, bool, error) {
	receivedAt = receivedAt.UTC()
	report, err := edgehealth.NormalizeReport(report, receivedAt)
	if err != nil {
		return EdgeHealthTransition{}, false, err
	}
	failedChecks := edgehealth.FailedCheckNames(report.Checks)
	failedJSON, err := json.Marshal(failedChecks)
	if err != nil {
		return EdgeHealthTransition{}, false, err
	}
	checksJSON, err := json.Marshal(report.Checks)
	if err != nil {
		return EdgeHealthTransition{}, false, err
	}
	previousStatus, err := s.edgeHealthStoredStatus(report.VPSName)
	if err != nil {
		return EdgeHealthTransition{}, false, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return EdgeHealthTransition{}, false, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
INSERT INTO edge_health_reports (
	vps_name, vps_public_ip, vps_internal_ip, status, failed_checks, checks_json,
	interval_seconds, missed_reports, checked_at, received_at, raw_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		report.VPSName,
		report.VPSPublicIP,
		report.VPSInternalIP,
		report.Status,
		string(failedJSON),
		string(checksJSON),
		report.IntervalSeconds,
		report.MissedReports,
		report.CheckedAt.Format(time.RFC3339Nano),
		receivedAt.Format(time.RFC3339Nano),
		string(raw),
	); err != nil {
		return EdgeHealthTransition{}, false, err
	}

	nowText := receivedAt.Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
INSERT INTO edge_health_expected (
	vps_name, vps_public_ip, vps_internal_ip, registered_at, bootstrap_until, updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(vps_name) DO UPDATE SET
	vps_public_ip = CASE WHEN excluded.vps_public_ip <> '' THEN excluded.vps_public_ip ELSE edge_health_expected.vps_public_ip END,
	vps_internal_ip = CASE WHEN excluded.vps_internal_ip <> '' THEN excluded.vps_internal_ip ELSE edge_health_expected.vps_internal_ip END,
	updated_at = excluded.updated_at`,
		report.VPSName,
		report.VPSPublicIP,
		report.VPSInternalIP,
		nowText,
		nowText,
		nowText,
	); err != nil {
		return EdgeHealthTransition{}, false, err
	}

	lastOKAt := ""
	if report.Status == edgehealth.StatusOK {
		lastOKAt = report.CheckedAt.Format(time.RFC3339Nano)
	}
	if _, err := tx.Exec(`
INSERT INTO edge_health_state (
	vps_name, vps_public_ip, vps_internal_ip, status, last_report_status,
	failed_checks, checks_json, interval_seconds, missed_reports, last_seen_at,
	last_ok_at, checked_at, updated_at, notified_status
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '')
ON CONFLICT(vps_name) DO UPDATE SET
	vps_public_ip = excluded.vps_public_ip,
	vps_internal_ip = excluded.vps_internal_ip,
	status = excluded.status,
	last_report_status = excluded.last_report_status,
	failed_checks = excluded.failed_checks,
	checks_json = excluded.checks_json,
	interval_seconds = excluded.interval_seconds,
	missed_reports = excluded.missed_reports,
	last_seen_at = excluded.last_seen_at,
	last_ok_at = CASE WHEN excluded.last_ok_at <> '' THEN excluded.last_ok_at ELSE edge_health_state.last_ok_at END,
	checked_at = excluded.checked_at,
	updated_at = excluded.updated_at`,
		report.VPSName,
		report.VPSPublicIP,
		report.VPSInternalIP,
		report.Status,
		report.Status,
		string(failedJSON),
		string(checksJSON),
		report.IntervalSeconds,
		report.MissedReports,
		receivedAt.Format(time.RFC3339Nano),
		lastOKAt,
		report.CheckedAt.Format(time.RFC3339Nano),
		nowText,
	); err != nil {
		return EdgeHealthTransition{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return EdgeHealthTransition{}, false, err
	}

	node := EdgeHealthNodeStatus{
		VPSName:          report.VPSName,
		VPSPublicIP:      report.VPSPublicIP,
		VPSInternalIP:    report.VPSInternalIP,
		Status:           report.Status,
		LastReportStatus: report.Status,
		FailedChecks:     failedChecks,
		Checks:           report.Checks,
		IntervalSeconds:  report.IntervalSeconds,
		MissedReports:    report.MissedReports,
		LastSeenAt:       receivedAt.Format(time.RFC3339Nano),
		LastOKAt:         lastOKAt,
		CheckedAt:        report.CheckedAt.Format(time.RFC3339Nano),
		UpdatedAt:        nowText,
		storedStatus:     previousStatus,
	}
	transition := EdgeHealthTransition{PreviousStatus: previousStatus, CurrentStatus: report.Status, Node: node}
	return transition, ShouldAlertEdgeHealthTransition(previousStatus, report.Status), nil
}

func (s *Store) EdgeHealthOverview(ctx context.Context, now time.Time, defaultInterval time.Duration, defaultMissedReports int, bootstrapGrace time.Duration) (EdgeHealthOverview, error) {
	if defaultInterval <= 0 {
		defaultInterval = edgehealth.DefaultInterval
	}
	if defaultMissedReports <= 0 {
		defaultMissedReports = edgehealth.DefaultMissedReports
	}
	if bootstrapGrace <= 0 {
		bootstrapGrace = DefaultEdgeHealthBootstrapGrace
	}
	now = now.UTC()
	rows, err := s.db.QueryContext(ctx, `
WITH names AS (
	SELECT vps_name FROM edge_health_expected
	UNION
	SELECT vps_name FROM edge_health_state
)
SELECT names.vps_name,
	coalesce(nullif(s.vps_public_ip, ''), e.vps_public_ip, '') AS vps_public_ip,
	coalesce(nullif(s.vps_internal_ip, ''), e.vps_internal_ip, '') AS vps_internal_ip,
	coalesce(s.status, '') AS stored_status,
	coalesce(s.last_report_status, '') AS last_report_status,
	coalesce(s.failed_checks, '[]') AS failed_checks,
	coalesce(s.checks_json, '[]') AS checks_json,
	coalesce(s.interval_seconds, 0) AS interval_seconds,
	coalesce(s.missed_reports, 0) AS missed_reports,
	coalesce(s.last_seen_at, '') AS last_seen_at,
	coalesce(s.last_ok_at, '') AS last_ok_at,
	coalesce(s.checked_at, '') AS checked_at,
	coalesce(s.updated_at, '') AS updated_at,
	coalesce(e.bootstrap_until, '') AS bootstrap_until
FROM names
LEFT JOIN edge_health_expected e ON e.vps_name = names.vps_name
LEFT JOIN edge_health_state s ON s.vps_name = names.vps_name
ORDER BY names.vps_name ASC`)
	if err != nil {
		return EdgeHealthOverview{}, err
	}
	defer rows.Close()

	overview := EdgeHealthOverview{
		GeneratedAt: now.Format(time.RFC3339Nano),
		Nodes:       make([]EdgeHealthNodeStatus, 0),
	}
	for rows.Next() {
		var node EdgeHealthNodeStatus
		var failedJSON, checksJSON string
		if err := rows.Scan(
			&node.VPSName,
			&node.VPSPublicIP,
			&node.VPSInternalIP,
			&node.storedStatus,
			&node.LastReportStatus,
			&failedJSON,
			&checksJSON,
			&node.IntervalSeconds,
			&node.MissedReports,
			&node.LastSeenAt,
			&node.LastOKAt,
			&node.CheckedAt,
			&node.UpdatedAt,
			&node.BootstrapUntil,
		); err != nil {
			return EdgeHealthOverview{}, err
		}
		node.FailedChecks = decodeStringList(failedJSON)
		node.Checks = decodeHealthChecks(checksJSON)
		node.Status, node.StaleAfter = effectiveEdgeHealthStatus(node, now, defaultInterval, defaultMissedReports, bootstrapGrace)
		countEdgeHealthStatus(&overview.Summary, node.Status)
		overview.Nodes = append(overview.Nodes, node)
	}
	if err := rows.Err(); err != nil {
		return EdgeHealthOverview{}, err
	}
	overview.Summary.Total = len(overview.Nodes)
	return overview, nil
}

func (s *Store) EvaluateEdgeHealthTransitions(ctx context.Context, now time.Time, defaultInterval time.Duration, defaultMissedReports int, bootstrapGrace time.Duration) ([]EdgeHealthTransition, error) {
	overview, err := s.EdgeHealthOverview(ctx, now, defaultInterval, defaultMissedReports, bootstrapGrace)
	if err != nil {
		return nil, err
	}
	transitions := make([]EdgeHealthTransition, 0)
	for _, node := range overview.Nodes {
		previous := node.storedStatus
		if previous == "" {
			previous = edgehealth.StatusUnknown
		}
		if previous == node.Status {
			continue
		}
		if err := s.updateEdgeHealthEffectiveStatus(node, now); err != nil {
			return transitions, err
		}
		if ShouldAlertEdgeHealthTransition(previous, node.Status) {
			transitions = append(transitions, EdgeHealthTransition{
				PreviousStatus: previous,
				CurrentStatus:  node.Status,
				Node:           node,
			})
		}
	}
	return transitions, nil
}

func (s *Store) MarkEdgeHealthNotified(vpsName, status string) error {
	_, err := s.db.Exec(`
UPDATE edge_health_state
SET notified_status = ?, updated_at = ?
WHERE vps_name = ?`,
		strings.TrimSpace(status),
		time.Now().UTC().Format(time.RFC3339Nano),
		strings.TrimSpace(vpsName),
	)
	return err
}

func ShouldAlertEdgeHealthTransition(previous, current string) bool {
	previous = strings.ToLower(strings.TrimSpace(previous))
	current = strings.ToLower(strings.TrimSpace(current))
	if previous == "" || previous == current {
		return false
	}
	switch current {
	case edgehealth.StatusDegraded:
		return previous == edgehealth.StatusOK
	case edgehealth.StatusDown:
		return previous == edgehealth.StatusOK || previous == edgehealth.StatusDegraded || previous == edgehealth.StatusUnknown
	case edgehealth.StatusOK:
		return previous == edgehealth.StatusDown || previous == edgehealth.StatusDegraded
	default:
		return false
	}
}

func (s *Store) edgeHealthStoredStatus(vpsName string) (string, error) {
	var status string
	err := s.db.QueryRow(`SELECT status FROM edge_health_state WHERE vps_name = ?`, strings.TrimSpace(vpsName)).Scan(&status)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return status, nil
}

func (s *Store) updateEdgeHealthEffectiveStatus(node EdgeHealthNodeStatus, now time.Time) error {
	checksJSON, _ := json.Marshal(node.Checks)
	failedJSON, _ := json.Marshal(node.FailedChecks)
	_, err := s.db.Exec(`
INSERT INTO edge_health_state (
	vps_name, vps_public_ip, vps_internal_ip, status, last_report_status,
	failed_checks, checks_json, interval_seconds, missed_reports, last_seen_at,
	last_ok_at, checked_at, updated_at, notified_status
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '')
ON CONFLICT(vps_name) DO UPDATE SET
	vps_public_ip = excluded.vps_public_ip,
	vps_internal_ip = excluded.vps_internal_ip,
	status = excluded.status,
	updated_at = excluded.updated_at`,
		node.VPSName,
		node.VPSPublicIP,
		node.VPSInternalIP,
		node.Status,
		node.LastReportStatus,
		string(failedJSON),
		string(checksJSON),
		node.IntervalSeconds,
		node.MissedReports,
		node.LastSeenAt,
		node.LastOKAt,
		node.CheckedAt,
		now.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func effectiveEdgeHealthStatus(node EdgeHealthNodeStatus, now time.Time, defaultInterval time.Duration, defaultMissedReports int, bootstrapGrace time.Duration) (string, string) {
	if strings.TrimSpace(node.LastSeenAt) == "" {
		if bootstrapUntil, ok := parseTime(node.BootstrapUntil); ok && now.Before(bootstrapUntil) {
			return edgehealth.StatusUnknown, ""
		}
		return edgehealth.StatusDown, ""
	}
	lastSeen, ok := parseTime(node.LastSeenAt)
	if !ok {
		return edgehealth.StatusDown, ""
	}
	interval := time.Duration(node.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = defaultInterval
	}
	missed := node.MissedReports
	if missed <= 0 {
		missed = defaultMissedReports
	}
	staleAfter := lastSeen.Add(interval * time.Duration(missed))
	if now.After(staleAfter) {
		return edgehealth.StatusDown, staleAfter.Format(time.RFC3339Nano)
	}
	reportStatus := strings.TrimSpace(node.LastReportStatus)
	if reportStatus == "" {
		reportStatus = strings.TrimSpace(node.storedStatus)
	}
	if reportStatus == "" {
		reportStatus = edgehealth.StatusUnknown
	}
	return reportStatus, staleAfter.Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, bool) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func decodeStringList(value string) []string {
	var out []string
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return nil
	}
	return out
}

func decodeHealthChecks(value string) []edgehealth.Check {
	var out []edgehealth.Check
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return nil
	}
	return out
}

func countEdgeHealthStatus(summary *EdgeHealthSummary, status string) {
	switch status {
	case edgehealth.StatusOK:
		summary.OK++
	case edgehealth.StatusDegraded:
		summary.Degraded++
	case edgehealth.StatusDown:
		summary.Down++
	default:
		summary.Unknown++
	}
}
