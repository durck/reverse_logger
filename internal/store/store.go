package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/durck/reverse_logger/internal/events"
	_ "modernc.org/sqlite"
)

type Store struct {
	db              *sql.DB
	eventsPath      string
	edgeLogPath     string
	ingressLogPath  string
	enrichedLogPath string
	correlation     CorrelationConfig
}

type CorrelationConfig struct {
	WebhookMatchBefore       time.Duration
	WebhookMatchAfter        time.Duration
	IngressReconcileBefore   time.Duration
	IngressReconcileAfter    time.Duration
	EnableClientIPFallback   bool
	EnableUniqueTimeFallback bool
}

func DefaultCorrelationConfig() CorrelationConfig {
	return CorrelationConfig{
		WebhookMatchBefore:       60 * time.Second,
		WebhookMatchAfter:        10 * time.Second,
		IngressReconcileBefore:   10 * time.Second,
		IngressReconcileAfter:    60 * time.Second,
		EnableClientIPFallback:   true,
		EnableUniqueTimeFallback: true,
	}
}

func (c CorrelationConfig) normalized() CorrelationConfig {
	defaults := DefaultCorrelationConfig()
	if c.WebhookMatchBefore <= 0 {
		c.WebhookMatchBefore = defaults.WebhookMatchBefore
	}
	if c.WebhookMatchAfter <= 0 {
		c.WebhookMatchAfter = defaults.WebhookMatchAfter
	}
	if c.IngressReconcileBefore <= 0 {
		c.IngressReconcileBefore = defaults.IngressReconcileBefore
	}
	if c.IngressReconcileAfter <= 0 {
		c.IngressReconcileAfter = defaults.IngressReconcileAfter
	}
	return c
}

func Open(dataDir string) (*Store, error) {
	if dataDir == "" {
		dataDir = "/data"
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", filepath.Join(dataDir, "events.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{
		db:              db,
		eventsPath:      filepath.Join(dataDir, "events.jsonl"),
		edgeLogPath:     filepath.Join(dataDir, "edge_events.jsonl"),
		ingressLogPath:  filepath.Join(dataDir, "ingress_events.jsonl"),
		enrichedLogPath: filepath.Join(dataDir, "enriched_events.jsonl"),
		correlation:     DefaultCorrelationConfig(),
	}

	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) SetCorrelationConfig(config CorrelationConfig) {
	s.correlation = config.normalized()
}

func (s *Store) init() error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}
	for _, stmt := range pragmas {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}

	schema := `
CREATE TABLE IF NOT EXISTS events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_hash TEXT NOT NULL UNIQUE,
	status TEXT NOT NULL,
	reverse_ssh_id TEXT,
	host_name TEXT,
	user_name TEXT,
	computer_name TEXT,
	ip_raw TEXT,
	ip_addr TEXT,
	ip_port INTEGER,
	transport TEXT,
	public_key_fingerprint TEXT,
	proxy_source_ip TEXT,
	version TEXT,
	source_ts TEXT,
	received_at TEXT NOT NULL,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS edge_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_hash TEXT NOT NULL UNIQUE,
	vps_name TEXT NOT NULL,
	vps_public_ip TEXT,
	vps_port INTEGER,
	client_ip TEXT NOT NULL,
	client_port INTEGER,
	received_at TEXT NOT NULL,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS ingress_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_hash TEXT NOT NULL UNIQUE,
	request_id TEXT,
	transport TEXT NOT NULL,
	vps_name TEXT NOT NULL,
	vps_public_ip TEXT,
	vps_internal_ip TEXT,
	forwarder_ip TEXT,
	client_ip TEXT NOT NULL,
	client_port INTEGER,
	host TEXT,
	uri TEXT NOT NULL,
	method TEXT NOT NULL,
	user_agent TEXT,
	upgrade_header TEXT,
	connection_header TEXT,
	x_forwarded_for TEXT,
	polling_key_sha1 TEXT,
	nginx_received_at TEXT NOT NULL,
	forwarded_at TEXT,
	raw_headers TEXT,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS enriched_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_hash TEXT NOT NULL UNIQUE,
	source_event_hash TEXT NOT NULL UNIQUE,
	ingress_event_hash TEXT,
	correlation_status TEXT NOT NULL,
	correlation_method TEXT,
	status TEXT NOT NULL,
	reverse_ssh_id TEXT,
	host_name TEXT,
	user_name TEXT,
	computer_name TEXT,
	ip_raw TEXT,
	ip_addr TEXT,
	ip_port INTEGER,
	real_client_ip TEXT,
	client_port INTEGER,
	transport TEXT,
	public_key_fingerprint TEXT,
	proxy_source_ip TEXT,
	vps_name TEXT,
	vps_public_ip TEXT,
	vps_internal_ip TEXT,
	forwarder_ip TEXT,
	version TEXT,
	source_ts TEXT,
	received_at TEXT NOT NULL,
	ingress_received_at TEXT,
	raw_webhook_json TEXT,
	raw_ingress_json TEXT
);`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	for _, column := range []struct {
		table string
		name  string
		def   string
	}{
		{table: "events", name: "transport", def: "TEXT"},
		{table: "events", name: "public_key_fingerprint", def: "TEXT"},
		{table: "events", name: "proxy_source_ip", def: "TEXT"},
		{table: "ingress_events", name: "forwarder_ip", def: "TEXT"},
		{table: "enriched_events", name: "correlation_method", def: "TEXT"},
		{table: "enriched_events", name: "public_key_fingerprint", def: "TEXT"},
		{table: "enriched_events", name: "proxy_source_ip", def: "TEXT"},
		{table: "enriched_events", name: "forwarder_ip", def: "TEXT"},
	} {
		if err := s.ensureColumn(column.table, column.name, column.def); err != nil {
			return err
		}
	}
	normalizers := []string{
		"UPDATE ingress_events SET forwarder_ip = '' WHERE forwarder_ip IS NULL",
		"UPDATE enriched_events SET correlation_method = '' WHERE correlation_method IS NULL",
		"UPDATE enriched_events SET forwarder_ip = '' WHERE forwarder_ip IS NULL",
	}
	for _, stmt := range normalizers {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_events_received_at ON events(received_at)",
		"CREATE INDEX IF NOT EXISTS idx_events_source_ts ON events(source_ts)",
		"CREATE INDEX IF NOT EXISTS idx_events_ip_addr ON events(ip_addr)",
		"CREATE INDEX IF NOT EXISTS idx_events_proxy_source_ip ON events(proxy_source_ip)",
		"CREATE INDEX IF NOT EXISTS idx_ingress_nginx_received_at ON ingress_events(nginx_received_at)",
		"CREATE INDEX IF NOT EXISTS idx_ingress_forwarded_at ON ingress_events(forwarded_at)",
		"CREATE INDEX IF NOT EXISTS idx_ingress_vps_internal_ip ON ingress_events(vps_internal_ip)",
		"CREATE INDEX IF NOT EXISTS idx_ingress_vps_public_ip ON ingress_events(vps_public_ip)",
		"CREATE INDEX IF NOT EXISTS idx_ingress_forwarder_ip ON ingress_events(forwarder_ip)",
		"CREATE INDEX IF NOT EXISTS idx_ingress_client_ip ON ingress_events(client_ip)",
		"CREATE INDEX IF NOT EXISTS idx_enriched_ingress_status ON enriched_events(ingress_event_hash, status)",
		"CREATE INDEX IF NOT EXISTS idx_enriched_reverse_status ON enriched_events(reverse_ssh_id, status, correlation_status)",
	}
	for _, stmt := range indexes {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) InsertEvent(event events.Event) (bool, error) {
	sourceTS := ""
	if !event.SourceTS.IsZero() {
		sourceTS = event.SourceTS.UTC().Format(time.RFC3339Nano)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
INSERT OR IGNORE INTO events (
	event_hash, status, reverse_ssh_id, host_name, user_name, computer_name,
	ip_raw, ip_addr, ip_port, transport, public_key_fingerprint, proxy_source_ip,
	version, source_ts, received_at, raw_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventHash,
		event.Status,
		event.ReverseSSHID,
		event.HostName,
		event.UserName,
		event.ComputerName,
		event.IPRaw,
		event.IPAddr,
		event.IPPort,
		event.Transport,
		event.PublicKeyFingerprint,
		event.ProxySourceIP,
		event.Version,
		sourceTS,
		event.ReceivedAt.UTC().Format(time.RFC3339Nano),
		string(event.RawJSON),
	)
	if err != nil {
		return false, err
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := appendJSONL(s.eventsPath, event); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) InsertEdgeEvent(event events.EdgeEvent) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
INSERT OR IGNORE INTO edge_events (
	event_hash, vps_name, vps_public_ip, vps_port,
	client_ip, client_port, received_at, raw_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventHash,
		event.VPSName,
		event.VPSPublicIP,
		event.VPSPort,
		event.ClientIP,
		event.ClientPort,
		event.ReceivedAt.UTC().Format(time.RFC3339Nano),
		string(event.RawJSON),
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := appendJSONL(s.edgeLogPath, event); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) InsertIngressEvent(event events.IngressEvent) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
INSERT OR IGNORE INTO ingress_events (
	event_hash, request_id, transport, vps_name, vps_public_ip, vps_internal_ip, forwarder_ip,
	client_ip, client_port, host, uri, method, user_agent, upgrade_header,
	connection_header, x_forwarded_for, polling_key_sha1, nginx_received_at,
	forwarded_at, raw_headers, raw_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventHash,
		event.RequestID,
		event.Transport,
		event.VPSName,
		event.VPSPublicIP,
		event.VPSInternalIP,
		event.ForwarderIP,
		event.ClientIP,
		event.ClientPort,
		event.Host,
		event.URI,
		event.Method,
		event.UserAgent,
		event.Upgrade,
		event.Connection,
		event.XForwardedFor,
		event.PollingKeySHA1,
		event.NginxReceivedAt.UTC().Format(time.RFC3339Nano),
		event.ForwardedAt.UTC().Format(time.RFC3339Nano),
		string(event.RawHeaders),
		string(event.RawJSON),
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := appendJSONL(s.ingressLogPath, event); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) EnrichAndStoreEvent(event events.Event) (events.EnrichedEvent, bool, error) {
	ingress, status, method, err := s.findIngressForEvent(event)
	if err != nil {
		return events.EnrichedEvent{}, false, err
	}
	enriched := events.NewEnrichedEventWithMethod(event, ingress, status, method)
	inserted, err := s.InsertEnrichedEvent(enriched)
	return enriched, inserted, err
}

func (s *Store) InsertEnrichedEvent(event events.EnrichedEvent) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	sourceTS := ""
	if !event.SourceTS.IsZero() {
		sourceTS = event.SourceTS.UTC().Format(time.RFC3339Nano)
	}
	ingressReceivedAt := ""
	if !event.IngressReceivedAt.IsZero() {
		ingressReceivedAt = event.IngressReceivedAt.UTC().Format(time.RFC3339Nano)
	}

	res, err := tx.Exec(`
INSERT INTO enriched_events (
	event_hash, source_event_hash, ingress_event_hash, correlation_status,
	correlation_method, status, reverse_ssh_id, host_name, user_name, computer_name, ip_raw, ip_addr,
	ip_port, real_client_ip, client_port, transport, public_key_fingerprint,
	proxy_source_ip, vps_name, vps_public_ip, vps_internal_ip, forwarder_ip, version,
	source_ts, received_at, ingress_received_at, raw_webhook_json, raw_ingress_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source_event_hash) DO UPDATE SET
	event_hash = excluded.event_hash,
	ingress_event_hash = excluded.ingress_event_hash,
	correlation_status = excluded.correlation_status,
	correlation_method = excluded.correlation_method,
	real_client_ip = excluded.real_client_ip,
	client_port = excluded.client_port,
	transport = excluded.transport,
	public_key_fingerprint = excluded.public_key_fingerprint,
	proxy_source_ip = excluded.proxy_source_ip,
	vps_name = excluded.vps_name,
	vps_public_ip = excluded.vps_public_ip,
	vps_internal_ip = excluded.vps_internal_ip,
	forwarder_ip = excluded.forwarder_ip,
	ingress_received_at = excluded.ingress_received_at,
	raw_ingress_json = excluded.raw_ingress_json
WHERE enriched_events.event_hash <> excluded.event_hash
  AND NOT (
    enriched_events.correlation_status = 'matched'
    AND (
      excluded.correlation_status <> 'matched'
      OR coalesce(enriched_events.ingress_event_hash, '') <> coalesce(excluded.ingress_event_hash, '')
    )
  )`,
		event.EventHash,
		event.SourceEventHash,
		event.IngressEventHash,
		event.CorrelationStatus,
		event.CorrelationMethod,
		event.Status,
		event.ReverseSSHID,
		event.HostName,
		event.UserName,
		event.ComputerName,
		event.IPRaw,
		event.IPAddr,
		event.IPPort,
		event.RealClientIP,
		event.ClientPort,
		event.Transport,
		event.PublicKeyFingerprint,
		event.ProxySourceIP,
		event.VPSName,
		event.VPSPublicIP,
		event.VPSInternalIP,
		event.ForwarderIP,
		event.Version,
		sourceTS,
		event.ReceivedAt.UTC().Format(time.RFC3339Nano),
		ingressReceivedAt,
		string(event.RawWebhookJSON),
		string(event.RawIngressJSON),
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := appendJSONL(s.enrichedLogPath, event); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ReconcileIngressEvent(ingress events.IngressEvent) (int, error) {
	windows := s.ingressReconcileWindows(ingress)
	count, err := s.reconcileIngressEvent(ingress, windows, true)
	if err != nil || count > 0 {
		return count, err
	}
	if !s.correlation.EnableClientIPFallback && !s.correlation.EnableUniqueTimeFallback {
		return count, nil
	}
	return s.reconcileIngressEvent(ingress, windows, false)
}

func (s *Store) reconcileIngressEvent(ingress events.IngressEvent, windows []timeWindow, requireIPMatch bool) (int, error) {
	args := make([]any, 0, len(windows)*4+8)
	timePredicate := buildEventTimePredicate(windows, &args)
	query := `
SELECT event_hash, status, reverse_ssh_id, host_name, user_name, computer_name,
	ip_raw, ip_addr, ip_port, transport, public_key_fingerprint, proxy_source_ip,
	version, source_ts, received_at, raw_json
FROM events
WHERE ` + timePredicate + `
`
	if requireIPMatch {
		predicate, predicateArgs := eventIPPredicateForIngress(ingress)
		if predicate == "" {
			return 0, nil
		}
		query += "  AND (" + predicate + ")\n"
		args = append(args, predicateArgs...)
	}
	query += "ORDER BY received_at DESC"
	rows, err := s.db.Query(strings.TrimSpace(query), args...)
	if err != nil {
		return 0, err
	}

	var candidates []events.Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return 0, err
		}
		candidates = append(candidates, event)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	count := 0
	for _, event := range candidates {
		_, changed, err := s.EnrichAndStoreEvent(event)
		if err != nil {
			return count, err
		}
		if changed {
			count++
		}
	}
	return count, nil
}

func (s *Store) findIngressForEvent(event events.Event) (*events.IngressEvent, string, string, error) {
	if event.Status == "disconnected" {
		ingress, ok, err := s.findConnectedIngressForClient(event.ReverseSSHID)
		if err != nil {
			return nil, "", "", err
		}
		if ok {
			return ingress, "matched", "connected-history", nil
		}
		return nil, "unmatched", "none", nil
	}
	windows := s.eventMatchWindows(event)
	matchIP := event.IPAddr
	primaryMethod := "vps-or-forwarder-ip"
	if event.ProxySourceIP != "" {
		matchIP = event.ProxySourceIP
		primaryMethod = "trusted-proxy"
	}
	if matchIP != "" {
		condition := "(vps_internal_ip = ? OR vps_public_ip = ? OR forwarder_ip = ?)"
		args := []any{matchIP, matchIP, matchIP}
		if event.ProxySourceIP != "" && event.IPAddr != "" {
			condition += " AND client_ip = ?"
			args = append(args, event.IPAddr)
		}
		candidates, err := s.findIngressCandidates(windows, condition, args, 2)
		if err != nil {
			return nil, "", "", err
		}
		if len(candidates) > 0 {
			return resolveIngressCandidates(candidates, primaryMethod)
		}
	}

	if s.correlation.EnableClientIPFallback && event.ProxySourceIP != "" && event.IPAddr != "" {
		condition := "client_ip = ?"
		args := []any{event.IPAddr}
		if event.Transport != "" {
			condition += " AND transport = ?"
			args = append(args, event.Transport)
		}
		candidates, err := s.findIngressCandidates(windows, condition, args, 2)
		if err != nil {
			return nil, "", "", err
		}
		if len(candidates) > 0 {
			return resolveIngressCandidates(candidates, "trusted-proxy-client-ip-fallback")
		}
	}

	if s.correlation.EnableUniqueTimeFallback && !(event.ProxySourceIP != "" && event.IPAddr != "") {
		condition := ""
		var args []any
		if event.Transport != "" {
			condition = "transport = ?"
			args = append(args, event.Transport)
		}
		candidates, err := s.findIngressCandidates(windows, condition, args, 2)
		if err != nil {
			return nil, "", "", err
		}
		if len(candidates) > 0 {
			return resolveIngressCandidates(candidates, "unique-time-fallback")
		}
	}

	return nil, "unmatched", "none", nil
}

type timeWindow struct {
	after  string
	before string
}

func (s *Store) eventMatchWindows(event events.Event) []timeWindow {
	times := []time.Time{event.ReceivedAt}
	if !event.SourceTS.IsZero() {
		times = append(times, event.SourceTS)
	}
	return buildTimeWindows(times, s.correlation.WebhookMatchBefore, s.correlation.WebhookMatchAfter)
}

func (s *Store) ingressReconcileWindows(ingress events.IngressEvent) []timeWindow {
	times := []time.Time{ingress.NginxReceivedAt}
	if !ingress.ForwardedAt.IsZero() {
		times = append(times, ingress.ForwardedAt)
	}
	return buildTimeWindows(times, s.correlation.IngressReconcileBefore, s.correlation.IngressReconcileAfter)
}

func buildTimeWindows(times []time.Time, before, after time.Duration) []timeWindow {
	if before <= 0 {
		before = time.Second
	}
	if after <= 0 {
		after = time.Second
	}
	seen := map[string]bool{}
	windows := make([]timeWindow, 0, len(times))
	for _, value := range times {
		if value.IsZero() {
			continue
		}
		value = value.UTC()
		key := value.Format(time.RFC3339Nano)
		if seen[key] {
			continue
		}
		seen[key] = true
		windows = append(windows, timeWindow{
			after:  value.Add(-before).Format(time.RFC3339Nano),
			before: value.Add(after).Format(time.RFC3339Nano),
		})
	}
	if len(windows) == 0 {
		now := time.Now().UTC()
		windows = append(windows, timeWindow{
			after:  now.Add(-before).Format(time.RFC3339Nano),
			before: now.Add(after).Format(time.RFC3339Nano),
		})
	}
	return windows
}

func buildIngressTimePredicate(windows []timeWindow, args *[]any) string {
	clauses := make([]string, 0, len(windows))
	for _, window := range windows {
		clauses = append(clauses, "(nginx_received_at BETWEEN ? AND ? OR forwarded_at BETWEEN ? AND ?)")
		*args = append(*args, window.after, window.before, window.after, window.before)
	}
	return "(" + strings.Join(clauses, " OR ") + ")"
}

func buildEventTimePredicate(windows []timeWindow, args *[]any) string {
	clauses := make([]string, 0, len(windows))
	for _, window := range windows {
		clauses = append(clauses, "(received_at BETWEEN ? AND ? OR source_ts BETWEEN ? AND ?)")
		*args = append(*args, window.after, window.before, window.after, window.before)
	}
	return "(" + strings.Join(clauses, " OR ") + ")"
}

func (s *Store) findIngressCandidates(windows []timeWindow, condition string, conditionArgs []any, limit int) ([]events.IngressEvent, error) {
	args := make([]any, 0, len(windows)*4+len(conditionArgs)+1)
	query := `
SELECT event_hash, request_id, transport, vps_name, vps_public_ip, vps_internal_ip,
	forwarder_ip, client_ip, client_port, host, uri, method, user_agent, upgrade_header,
	connection_header, x_forwarded_for, polling_key_sha1, nginx_received_at,
	forwarded_at, raw_headers, raw_json
FROM ingress_events
WHERE ` + buildIngressTimePredicate(windows, &args) + `
`
	if strings.TrimSpace(condition) != "" {
		query += "  AND (" + condition + ")\n"
		args = append(args, conditionArgs...)
	}
	query += `  AND NOT EXISTS (
    SELECT 1 FROM enriched_events
    WHERE enriched_events.ingress_event_hash = ingress_events.event_hash
      AND enriched_events.status = 'connected'
  )
ORDER BY coalesce(nullif(forwarded_at, ''), nginx_received_at) DESC, nginx_received_at DESC`
	if limit > 0 {
		query += "\nLIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []events.IngressEvent
	for rows.Next() {
		ingress, err := scanIngressEvent(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, ingress)
	}
	return candidates, rows.Err()
}

func resolveIngressCandidates(candidates []events.IngressEvent, method string) (*events.IngressEvent, string, string, error) {
	switch len(candidates) {
	case 0:
		return nil, "unmatched", "none", nil
	case 1:
		return &candidates[0], "matched", method, nil
	default:
		return nil, "ambiguous", method, nil
	}
}

func eventIPPredicateForIngress(ingress events.IngressEvent) (string, []any) {
	ips := uniqueNonEmpty(ingress.VPSInternalIP, ingress.VPSPublicIP, ingress.ForwarderIP, ingress.ClientIP)
	if len(ips) == 0 {
		return "", nil
	}
	clauses := make([]string, 0, len(ips))
	args := make([]any, 0, len(ips)*2)
	for _, ip := range ips {
		clauses = append(clauses, "(ip_addr = ? OR proxy_source_ip = ?)")
		args = append(args, ip, ip)
	}
	return strings.Join(clauses, " OR "), args
}

func uniqueNonEmpty(values ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func (s *Store) findConnectedIngressForClient(reverseSSHID string) (*events.IngressEvent, bool, error) {
	if reverseSSHID == "" {
		return nil, false, nil
	}
	row := s.db.QueryRow(`
SELECT ie.event_hash, ie.request_id, ie.transport, ie.vps_name, ie.vps_public_ip,
	ie.vps_internal_ip, ie.forwarder_ip, ie.client_ip, ie.client_port, ie.host, ie.uri, ie.method,
	ie.user_agent, ie.upgrade_header, ie.connection_header, ie.x_forwarded_for,
	ie.polling_key_sha1, ie.nginx_received_at, ie.forwarded_at, ie.raw_headers, ie.raw_json
FROM enriched_events ee
JOIN ingress_events ie ON ie.event_hash = ee.ingress_event_hash
WHERE ee.reverse_ssh_id = ?
  AND ee.status = 'connected'
  AND ee.correlation_status = 'matched'
ORDER BY ee.received_at DESC
LIMIT 1`, reverseSSHID)
	ingress, err := scanIngressEvent(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &ingress, true, nil
}

func (s *Store) Ping() error {
	return s.db.Ping()
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) ensureColumn(table, name, definition string) error {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var columnName, columnType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if columnName == name {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec("ALTER TABLE " + table + " ADD COLUMN " + name + " " + definition)
	return err
}

func appendJSONL(path string, value any) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()

	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("append %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanIngressEvent(row rowScanner) (events.IngressEvent, error) {
	var event events.IngressEvent
	var nginxReceivedAt, forwardedAt string
	var forwarderIP sql.NullString
	var rawHeaders, rawJSON string
	if err := row.Scan(
		&event.EventHash,
		&event.RequestID,
		&event.Transport,
		&event.VPSName,
		&event.VPSPublicIP,
		&event.VPSInternalIP,
		&forwarderIP,
		&event.ClientIP,
		&event.ClientPort,
		&event.Host,
		&event.URI,
		&event.Method,
		&event.UserAgent,
		&event.Upgrade,
		&event.Connection,
		&event.XForwardedFor,
		&event.PollingKeySHA1,
		&nginxReceivedAt,
		&forwardedAt,
		&rawHeaders,
		&rawJSON,
	); err != nil {
		return events.IngressEvent{}, err
	}
	event.ForwarderIP = forwarderIP.String
	if nginxReceivedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, nginxReceivedAt); err == nil {
			event.NginxReceivedAt = parsed
		}
	}
	if forwardedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, forwardedAt); err == nil {
			event.ForwardedAt = parsed
		}
	}
	if rawHeaders != "" {
		event.RawHeaders = []byte(rawHeaders)
	}
	if rawJSON != "" {
		event.RawJSON = []byte(rawJSON)
	}
	return event, nil
}

func scanEvent(row rowScanner) (events.Event, error) {
	var event events.Event
	var sourceTS, receivedAt, rawJSON string
	if err := row.Scan(
		&event.EventHash,
		&event.Status,
		&event.ReverseSSHID,
		&event.HostName,
		&event.UserName,
		&event.ComputerName,
		&event.IPRaw,
		&event.IPAddr,
		&event.IPPort,
		&event.Transport,
		&event.PublicKeyFingerprint,
		&event.ProxySourceIP,
		&event.Version,
		&sourceTS,
		&receivedAt,
		&rawJSON,
	); err != nil {
		return events.Event{}, err
	}
	if sourceTS != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, sourceTS); err == nil {
			event.SourceTS = parsed
		}
	}
	if receivedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, receivedAt); err == nil {
			event.ReceivedAt = parsed
		}
	}
	if rawJSON != "" {
		event.RawJSON = []byte(rawJSON)
	}
	return event, nil
}
