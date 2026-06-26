package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/durck/reverse_logger/internal/events"
)

func TestInsertEventDedupesJSONLAndSQLite(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	event, err := events.ParseWebhookPayload([]byte(`{"Status":"connected","ID":"abc","IP":"192.0.2.1:1234","HostName":"u.c","Timestamp":"2026-06-09T12:00:00Z"}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	inserted, err := st.InsertEvent(event)
	if err != nil || !inserted {
		t.Fatalf("first insert inserted=%v err=%v", inserted, err)
	}
	inserted, err = st.InsertEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Fatal("duplicate event was inserted")
	}

	content, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := countLines(content); lines != 1 {
		t.Fatalf("events.jsonl lines = %d", lines)
	}
}

func TestInsertEventRollsBackSQLiteWhenJSONLAppendFails(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	event, err := events.ParseWebhookPayload([]byte(`{"Status":"connected","ID":"abc","IP":"192.0.2.1:1234","HostName":"u.c","Timestamp":"2026-06-09T12:00:00Z"}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "events.jsonl"), 0o750); err != nil {
		t.Fatal(err)
	}

	inserted, err := st.InsertEvent(event)
	if err == nil {
		t.Fatal("expected JSONL append failure")
	}
	if inserted {
		t.Fatal("event reported inserted after JSONL append failure")
	}

	var count int
	if err := st.db.QueryRow(`select count(*) from events where event_hash = ?`, event.EventHash).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("SQLite row count after rollback = %d", count)
	}
}

func TestInsertEdgeEventRollsBackSQLiteWhenJSONLAppendFails(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	event := events.NewEdgeEvent("vps-1", "203.0.113.20", 3232, "198.51.100.10", 5555, time.Now(), nil)
	if err := os.Mkdir(filepath.Join(dir, "edge_events.jsonl"), 0o750); err != nil {
		t.Fatal(err)
	}

	inserted, err := st.InsertEdgeEvent(event)
	if err == nil {
		t.Fatal("expected edge JSONL append failure")
	}
	if inserted {
		t.Fatal("edge event reported inserted after JSONL append failure")
	}

	var count int
	if err := st.db.QueryRow(`select count(*) from edge_events where event_hash = ?`, event.EventHash).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("SQLite edge row count after rollback = %d", count)
	}
}

func TestEnrichHandlesOldIngressRowsWithNullForwarderIP(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	_, err = st.db.Exec(`
INSERT INTO ingress_events (
	event_hash, request_id, transport, vps_name, vps_public_ip, vps_internal_ip,
	client_ip, client_port, host, uri, method, user_agent, upgrade_header,
	connection_header, x_forwarded_for, polling_key_sha1, nginx_received_at,
	forwarded_at, raw_headers, raw_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"old-ingress-with-null-forwarder-ip",
		"",
		"wss",
		"vps-1",
		"",
		"192.0.2.2",
		"198.51.100.10",
		5555,
		"",
		"/ws",
		"GET",
		"",
		"websocket",
		"",
		"",
		"",
		"2026-06-09T12:00:00Z",
		"2026-06-09T12:00:00Z",
		"",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}

	event, err := events.ParseWebhookPayload([]byte(`{"Status":"connected","ID":"abc","IP":"192.0.2.2:1234","HostName":"u.c","Timestamp":"2026-06-09T12:00:05Z"}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	enriched, _, err := st.EnrichAndStoreEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	if enriched.CorrelationStatus != "matched" {
		t.Fatalf("correlation_status = %q", enriched.CorrelationStatus)
	}
}

func countLines(content []byte) int {
	lines := 0
	for _, b := range content {
		if b == '\n' {
			lines++
		}
	}
	return lines
}
