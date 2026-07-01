package store

import (
	"context"
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

func TestDashboardOverviewEmptyStoreReturnsZeroSummary(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	overview, err := st.DashboardOverview(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Totals.Total != 0 || overview.Totals.Matched != 0 || overview.Totals.Unmatched != 0 {
		t.Fatalf("unexpected totals: %+v", overview.Totals)
	}
	if len(overview.Timeline) == 0 {
		t.Fatal("expected zero-filled timeline")
	}
}

func TestDashboardOverviewSummarizesEnrichedEvents(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now().UTC()
	seedDashboardEnriched(t, st, "one", "connected", "matched", "wss", "edge-1", "alice.workstation", "198.51.100.10", "203.0.113.10", "edge1.example.com", now.Add(-30*time.Minute))
	seedDashboardEnriched(t, st, "two", "disconnected", "matched", "wss", "edge-1", "alice.workstation", "198.51.100.10", "203.0.113.10", "edge1.example.com", now.Add(-25*time.Minute))
	seedDashboardEnriched(t, st, "three", "connected", "unmatched", "https", "edge-2", "bob.laptop", "", "203.0.113.20", "edge2.example.com", now.Add(-20*time.Minute))

	overview, err := st.DashboardOverview(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Totals.Total != 3 || overview.Totals.Connected != 2 || overview.Totals.Disconnected != 1 {
		t.Fatalf("unexpected status totals: %+v", overview.Totals)
	}
	if overview.Totals.Matched != 2 || overview.Totals.Unmatched != 1 || overview.Totals.Ambiguous != 0 {
		t.Fatalf("unexpected correlation totals: %+v", overview.Totals)
	}
	if countForName(overview.ByTransport, "wss") != 2 || countForName(overview.ByTransport, "https") != 1 {
		t.Fatalf("unexpected transport counts: %+v", overview.ByTransport)
	}
	if countForName(overview.ByVPS, "edge-1") != 2 || countForName(overview.ByVPS, "edge-2") != 1 {
		t.Fatalf("unexpected vps counts: %+v", overview.ByVPS)
	}
}

func TestDashboardOverviewReportsActiveSessions(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now().UTC()
	seedDashboardEnrichedForClient(t, st, "live-open", "client-live", "connected", "matched", "wss", "edge-1", "live.host", "198.51.100.10", "203.0.113.10", "edge1.example.com", now.Add(-90*time.Minute))
	seedDashboardEnrichedForClient(t, st, "closed-open", "client-closed", "connected", "matched", "wss", "edge-1", "closed.host", "198.51.100.11", "203.0.113.10", "edge1.example.com", now.Add(-80*time.Minute))
	seedDashboardEnrichedForClient(t, st, "closed-close", "client-closed", "disconnected", "matched", "wss", "edge-1", "closed.host", "198.51.100.11", "203.0.113.10", "edge1.example.com", now.Add(-10*time.Minute))

	overview, err := st.DashboardOverview(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Totals.Active != 1 {
		t.Fatalf("active total = %d, totals=%+v", overview.Totals.Active, overview.Totals)
	}
	if len(overview.ActiveSessions) != 1 {
		t.Fatalf("active sessions length = %d, sessions=%+v", len(overview.ActiveSessions), overview.ActiveSessions)
	}
	if overview.ActiveSessions[0].ReverseSSHID != "client-live" {
		t.Fatalf("unexpected active session: %+v", overview.ActiveSessions[0])
	}
	if len(overview.Timeline) == 0 || overview.Timeline[len(overview.Timeline)-1].Active < overview.Totals.Active {
		t.Fatalf("unexpected active timeline tail: %+v", overview.Timeline)
	}
}

func TestDashboardTimelineReportsPeakActiveSessionsWithinLongWindowBucket(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now().UTC()
	seedDashboardEnrichedForClient(t, st, "stale-open", "client-stale", "connected", "matched", "wss", "edge-1", "stale.host", "198.51.100.20", "203.0.113.10", "edge1.example.com", now.Add(-60*24*time.Hour))
	seedDashboardEnrichedForClient(t, st, "stale-close", "client-stale", "disconnected", "matched", "wss", "edge-1", "stale.host", "198.51.100.20", "203.0.113.10", "edge1.example.com", now.Add(-45*24*time.Hour))
	seedDashboardEnrichedForClient(t, st, "short-open", "client-short", "connected", "matched", "wss", "edge-1", "short.host", "198.51.100.21", "203.0.113.10", "edge1.example.com", now.Add(-72*time.Hour))
	seedDashboardEnrichedForClient(t, st, "short-close", "client-short", "disconnected", "matched", "wss", "edge-1", "short.host", "198.51.100.21", "203.0.113.10", "edge1.example.com", now.Add(-71*time.Hour-45*time.Minute))

	overview, err := st.DashboardOverview(context.Background(), 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Totals.Active != 0 {
		t.Fatalf("active total = %d, totals=%+v", overview.Totals.Active, overview.Totals)
	}
	if len(overview.Timeline) < 80 {
		t.Fatalf("expected sub-day buckets for long window, got %d", len(overview.Timeline))
	}
	if overview.Timeline[0].Active != 0 {
		t.Fatalf("stale pre-window session leaked into first bucket: %+v", overview.Timeline[0])
	}
	hasShortSessionPeak := false
	for _, bucket := range overview.Timeline {
		if bucket.Active > 0 {
			hasShortSessionPeak = true
			break
		}
	}
	if !hasShortSessionPeak {
		t.Fatalf("short session peak was not represented in timeline: %+v", overview.Timeline)
	}
}

func TestDashboardEventsFiltersAndSearches(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now().UTC()
	seedDashboardEnriched(t, st, "one", "connected", "matched", "wss", "edge-1", "alice.workstation", "198.51.100.10", "203.0.113.10", "edge1.example.com", now.Add(-30*time.Minute))
	seedDashboardEnriched(t, st, "two", "connected", "unmatched", "https", "edge-2", "bob.laptop", "203.0.113.10", "203.0.113.20", "edge2.example.com", now.Add(-20*time.Minute))

	events, err := st.DashboardEvents(context.Background(), DashboardEventQuery{
		Window:            24 * time.Hour,
		Status:            "connected",
		CorrelationStatus: "matched",
		Transport:         "wss",
		Search:            "edge1.example.com",
		Limit:             10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events length = %d, events=%+v", len(events), events)
	}
	if events[0].HostName != "alice.workstation" || events[0].RealClientIP != "198.51.100.10" {
		t.Fatalf("unexpected event: %+v", events[0])
	}
	if events[0].VPSPublicIP != "203.0.113.10" || events[0].IngressHost != "edge1.example.com" {
		t.Fatalf("unexpected event: %+v", events[0])
	}
}

func seedDashboardEnriched(t *testing.T, st *Store, suffix, status, correlationStatus, transport, vpsName, hostName, realClientIP, vpsPublicIP, ingressHost string, receivedAt time.Time) {
	t.Helper()
	seedDashboardEnrichedForClient(t, st, suffix, "client-"+suffix, status, correlationStatus, transport, vpsName, hostName, realClientIP, vpsPublicIP, ingressHost, receivedAt)
}

func seedDashboardEnrichedForClient(t *testing.T, st *Store, suffix, reverseSSHID, status, correlationStatus, transport, vpsName, hostName, realClientIP, vpsPublicIP, ingressHost string, receivedAt time.Time) {
	t.Helper()
	ingressHash := "dashboard-ingress-" + suffix
	_, err := st.db.Exec(`
INSERT INTO ingress_events (
	event_hash, transport, vps_name, vps_public_ip, vps_internal_ip,
	client_ip, client_port, host, uri, method, upgrade_header,
	nginx_received_at, forwarded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ingressHash,
		transport,
		vpsName,
		vpsPublicIP,
		"10.21.125.98",
		realClientIP,
		5555,
		ingressHost,
		"/ws",
		"GET",
		"websocket",
		receivedAt.UTC().Format(time.RFC3339Nano),
		receivedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.db.Exec(`
INSERT INTO enriched_events (
	event_hash, source_event_hash, ingress_event_hash, correlation_status, correlation_method,
	status, reverse_ssh_id, host_name, user_name, computer_name, ip_raw,
	ip_addr, real_client_ip, transport, vps_name, vps_public_ip,
	vps_internal_ip, forwarder_ip, received_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"dashboard-event-"+suffix,
		"dashboard-source-"+suffix,
		ingressHash,
		correlationStatus,
		"test",
		status,
		reverseSSHID,
		hostName,
		"operator",
		hostName,
		"192.0.2.10:5000",
		"192.0.2.10",
		realClientIP,
		transport,
		vpsName,
		vpsPublicIP,
		"10.21.125.98",
		"10.21.125.98",
		receivedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatal(err)
	}
}

func countForName(counts []DashboardCount, name string) int {
	for _, count := range counts {
		if count.Name == name {
			return count.Count
		}
	}
	return 0
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
