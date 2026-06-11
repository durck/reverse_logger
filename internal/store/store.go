package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	store := &Store{
		db:              db,
		eventsPath:      filepath.Join(dataDir, "events.jsonl"),
		edgeLogPath:     filepath.Join(dataDir, "edge_events.jsonl"),
		ingressLogPath:  filepath.Join(dataDir, "ingress_events.jsonl"),
		enrichedLogPath: filepath.Join(dataDir, "enriched_events.jsonl"),
	}

	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
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
		{table: "enriched_events", name: "public_key_fingerprint", def: "TEXT"},
		{table: "enriched_events", name: "proxy_source_ip", def: "TEXT"},
	} {
		if err := s.ensureColumn(column.table, column.name, column.def); err != nil {
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
	event_hash, request_id, transport, vps_name, vps_public_ip, vps_internal_ip,
	client_ip, client_port, host, uri, method, user_agent, upgrade_header,
	connection_header, x_forwarded_for, polling_key_sha1, nginx_received_at,
	forwarded_at, raw_headers, raw_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventHash,
		event.RequestID,
		event.Transport,
		event.VPSName,
		event.VPSPublicIP,
		event.VPSInternalIP,
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
	ingress, status, err := s.findIngressForEvent(event)
	if err != nil {
		return events.EnrichedEvent{}, false, err
	}
	enriched := events.NewEnrichedEvent(event, ingress, status)
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
	status, reverse_ssh_id, host_name, user_name, computer_name, ip_raw, ip_addr,
	ip_port, real_client_ip, client_port, transport, public_key_fingerprint,
	proxy_source_ip, vps_name, vps_public_ip, vps_internal_ip, version,
	source_ts, received_at, ingress_received_at, raw_webhook_json, raw_ingress_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source_event_hash) DO UPDATE SET
	event_hash = excluded.event_hash,
	ingress_event_hash = excluded.ingress_event_hash,
	correlation_status = excluded.correlation_status,
	real_client_ip = excluded.real_client_ip,
	client_port = excluded.client_port,
	transport = excluded.transport,
	public_key_fingerprint = excluded.public_key_fingerprint,
	proxy_source_ip = excluded.proxy_source_ip,
	vps_name = excluded.vps_name,
	vps_public_ip = excluded.vps_public_ip,
	vps_internal_ip = excluded.vps_internal_ip,
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
	after := ingress.NginxReceivedAt.Add(-10 * time.Second).UTC().Format(time.RFC3339Nano)
	before := ingress.NginxReceivedAt.Add(60 * time.Second).UTC().Format(time.RFC3339Nano)
	rows, err := s.db.Query(`
SELECT event_hash, status, reverse_ssh_id, host_name, user_name, computer_name,
	ip_raw, ip_addr, ip_port, transport, public_key_fingerprint, proxy_source_ip,
	version, source_ts, received_at, raw_json
FROM events
WHERE (received_at BETWEEN ? AND ? OR source_ts BETWEEN ? AND ?)
  AND (ip_addr = ? OR ip_addr = ? OR proxy_source_ip = ? OR proxy_source_ip = ?)
ORDER BY received_at DESC`, after, before, after, before, ingress.VPSInternalIP, ingress.VPSPublicIP, ingress.VPSInternalIP, ingress.VPSPublicIP)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return count, err
		}
		_, changed, err := s.EnrichAndStoreEvent(event)
		if err != nil {
			return count, err
		}
		if changed {
			count++
		}
	}
	return count, rows.Err()
}

func (s *Store) findIngressForEvent(event events.Event) (*events.IngressEvent, string, error) {
	if event.Status == "disconnected" {
		ingress, ok, err := s.findConnectedIngressForClient(event.ReverseSSHID)
		if err != nil {
			return nil, "", err
		}
		if ok {
			return ingress, "matched", nil
		}
		return nil, "unmatched", nil
	}
	matchIP := event.IPAddr
	if event.ProxySourceIP != "" {
		matchIP = event.ProxySourceIP
	}
	if matchIP == "" {
		return nil, "unmatched", nil
	}
	baseTime := event.ReceivedAt
	if !event.SourceTS.IsZero() {
		baseTime = event.SourceTS
	}
	after := baseTime.Add(-60 * time.Second).UTC().Format(time.RFC3339Nano)
	before := baseTime.Add(10 * time.Second).UTC().Format(time.RFC3339Nano)
	query := `
SELECT event_hash, request_id, transport, vps_name, vps_public_ip, vps_internal_ip,
	client_ip, client_port, host, uri, method, user_agent, upgrade_header,
	connection_header, x_forwarded_for, polling_key_sha1, nginx_received_at,
	forwarded_at, raw_headers, raw_json
FROM ingress_events
WHERE nginx_received_at BETWEEN ? AND ?
  AND (vps_internal_ip = ? OR vps_public_ip = ?)
`
	args := []any{after, before, matchIP, matchIP}
	if event.ProxySourceIP != "" && event.IPAddr != "" {
		query += "  AND client_ip = ?\n"
		args = append(args, event.IPAddr)
	}
	query += `  AND NOT EXISTS (
    SELECT 1 FROM enriched_events
    WHERE enriched_events.ingress_event_hash = ingress_events.event_hash
      AND enriched_events.status = 'connected'
  )
ORDER BY nginx_received_at DESC
LIMIT 2`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var candidates []events.IngressEvent
	for rows.Next() {
		ingress, err := scanIngressEvent(rows)
		if err != nil {
			return nil, "", err
		}
		candidates = append(candidates, ingress)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	switch len(candidates) {
	case 0:
		return nil, "unmatched", nil
	case 1:
		return &candidates[0], "matched", nil
	default:
		return nil, "ambiguous", nil
	}
}

func (s *Store) findConnectedIngressForClient(reverseSSHID string) (*events.IngressEvent, bool, error) {
	if reverseSSHID == "" {
		return nil, false, nil
	}
	row := s.db.QueryRow(`
SELECT ie.event_hash, ie.request_id, ie.transport, ie.vps_name, ie.vps_public_ip,
	ie.vps_internal_ip, ie.client_ip, ie.client_port, ie.host, ie.uri, ie.method,
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
	var rawHeaders, rawJSON string
	if err := row.Scan(
		&event.EventHash,
		&event.RequestID,
		&event.Transport,
		&event.VPSName,
		&event.VPSPublicIP,
		&event.VPSInternalIP,
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
