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

func TestInsertReverseSSHErrorEventDedupesJSONLAndSQLite(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	event, err := events.NormalizeReverseSSHErrorEvent(events.ReverseSSHErrorEvent{
		Source:     "journalctl",
		Unit:       "reverse_ssh",
		Message:    "public key fingerprint mismatch from 198.51.100.10:53000",
		RemoteAddr: "198.51.100.10:53000",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	inserted, err := st.InsertReverseSSHErrorEvent(event)
	if err != nil || !inserted {
		t.Fatalf("first insert inserted=%v err=%v", inserted, err)
	}
	inserted, err = st.InsertReverseSSHErrorEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Fatal("duplicate reverse_ssh error event was inserted")
	}

	content, err := os.ReadFile(filepath.Join(dir, "reverse_ssh_errors.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := countLines(content); lines != 1 {
		t.Fatalf("reverse_ssh_errors.jsonl lines = %d", lines)
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

func TestEnrichFallsBackToUniqueIngressWhenProxyMetadataDoesNotMatch(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Date(2026, 7, 8, 13, 50, 33, 0, time.UTC)
	ingress, err := events.NormalizeIngressEvent(events.IngressEvent{
		Transport:       "wss",
		VPSName:         "edge-1",
		ClientIP:        "198.51.100.10",
		ClientPort:      53000,
		Host:            "glonastracking.ru",
		URI:             "/ws",
		Method:          "GET",
		Upgrade:         "websocket",
		NginxReceivedAt: now.Add(-2 * time.Second),
	}, now.Add(-2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if inserted, err := st.InsertIngressEvent(ingress); err != nil || !inserted {
		t.Fatalf("insert ingress inserted=%v err=%v", inserted, err)
	}

	webhook := []byte(`{"Status":"connected","ID":"abc","IP":"10.21.125.98:55644","HostName":"lab.administrator.ws10","Transport":"wss","ProxySourceIP":"203.0.113.20:443","Timestamp":"2026-07-08T13:50:33Z"}`)
	event, err := events.ParseWebhookPayload(webhook, now)
	if err != nil {
		t.Fatal(err)
	}
	enriched, _, err := st.EnrichAndStoreEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	if enriched.CorrelationStatus != "matched" || enriched.CorrelationMethod != "unique-time-fallback" {
		t.Fatalf("correlation = %s/%s, enriched=%+v", enriched.CorrelationStatus, enriched.CorrelationMethod, enriched)
	}
	if enriched.IngressEventHash != ingress.EventHash || enriched.RealClientIP != ingress.ClientIP || enriched.VPSName != ingress.VPSName {
		t.Fatalf("ingress metadata was not attached: %+v", enriched)
	}
}

func TestEnrichHTTPSFallsBackWhenWebhookReportsProxyAsClient(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Date(2026, 7, 8, 14, 20, 26, 0, time.UTC)
	ingress, err := events.NormalizeIngressEvent(events.IngressEvent{
		Transport:       "https",
		VPSName:         "edge-1",
		ForwarderIP:     "10.21.125.98",
		ClientIP:        "212.73.99.133",
		ClientPort:      53000,
		Host:            "updatewebrtc.ru",
		URI:             "/push?key=abcdef",
		Method:          "HEAD",
		PollingKeySHA1:  "a9993e364706816aba3e25717850c26c9cd0d89d",
		NginxReceivedAt: now.Add(-2 * time.Second),
		ForwardedAt:     now.Add(-time.Second),
	}, now.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if inserted, err := st.InsertIngressEvent(ingress); err != nil || !inserted {
		t.Fatalf("insert ingress inserted=%v err=%v", inserted, err)
	}

	webhook := []byte(`{"Status":"connected","ID":"abc","IP":"10.21.125.98:55644","HostName":"wdc01-syorlov.danii.wdc01-syorlov","Transport":"https","ProxySourceIP":"10.21.125.98:443","Timestamp":"2026-07-08T14:20:26Z"}`)
	event, err := events.ParseWebhookPayload(webhook, now)
	if err != nil {
		t.Fatal(err)
	}
	enriched, _, err := st.EnrichAndStoreEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	if enriched.CorrelationStatus != "matched" || enriched.CorrelationMethod != "unique-time-fallback" {
		t.Fatalf("correlation = %s/%s, enriched=%+v", enriched.CorrelationStatus, enriched.CorrelationMethod, enriched)
	}
	if enriched.RealClientIP != ingress.ClientIP || enriched.IngressEventHash != ingress.EventHash || enriched.Transport != "https" {
		t.Fatalf("https ingress metadata was not attached: %+v", enriched)
	}
}

func TestEnrichHTTPSChoosesNearestIngressWhenOldCandidateIsInWindow(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Date(2026, 7, 8, 19, 49, 17, 0, time.UTC)
	oldIngress, err := events.NormalizeIngressEvent(events.IngressEvent{
		Transport:       "https",
		VPSName:         "updatewebrtc",
		VPSPublicIP:     "201.51.5.226",
		ForwarderIP:     "201.51.5.226",
		ClientIP:        "94.25.172.61",
		ClientPort:      53000,
		Host:            "updatewebrtc.ru",
		URI:             "/push?key=old",
		Method:          "HEAD",
		PollingKeySHA1:  "1f8ac10f23c5b5bc1167bda84b833e5c057a77d2",
		NginxReceivedAt: now.Add(-67 * time.Second),
		ForwardedAt:     now.Add(-30 * time.Second),
	}, now.Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if inserted, err := st.InsertIngressEvent(oldIngress); err != nil || !inserted {
		t.Fatalf("insert old ingress inserted=%v err=%v", inserted, err)
	}

	nearestIngress, err := events.NormalizeIngressEvent(events.IngressEvent{
		Transport:       "https",
		VPSName:         "updatewebrtc",
		VPSPublicIP:     "201.51.5.226",
		ForwarderIP:     "201.51.5.226",
		ClientIP:        "212.73.99.133",
		ClientPort:      55555,
		Host:            "updatewebrtc.ru",
		URI:             "/push?key=new",
		Method:          "HEAD",
		PollingKeySHA1:  "a9993e364706816aba3e25717850c26c9cd0d89d",
		NginxReceivedAt: now.Add(-1 * time.Second),
		ForwardedAt:     now.Add(-1 * time.Second),
	}, now.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if inserted, err := st.InsertIngressEvent(nearestIngress); err != nil || !inserted {
		t.Fatalf("insert nearest ingress inserted=%v err=%v", inserted, err)
	}

	webhook := []byte(`{"Status":"connected","ID":"abc","IP":"201.51.5.226:443","HostName":"wdc01-syorlov.danii.wdc01-syorlov","Transport":"https","Timestamp":"2026-07-08T19:49:17Z"}`)
	event, err := events.ParseWebhookPayload(webhook, now)
	if err != nil {
		t.Fatal(err)
	}
	enriched, _, err := st.EnrichAndStoreEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	if enriched.CorrelationStatus != "matched" || enriched.IngressEventHash != nearestIngress.EventHash {
		t.Fatalf("expected nearest ingress match, got correlation=%s ingress=%s enriched=%+v", enriched.CorrelationStatus, enriched.IngressEventHash, enriched)
	}
	if enriched.RealClientIP != nearestIngress.ClientIP {
		t.Fatalf("real client ip = %q, want %q", enriched.RealClientIP, nearestIngress.ClientIP)
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
	seedDashboardEnrichedForClient(t, st, "live-open", "client-live", "connected", "matched", "wss", "edge-1", "live.host", "198.51.100.10", "203.0.113.10", "edge1.example.com", now.Add(-30*time.Minute))
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
	if overview.Timeline[len(overview.Timeline)-1].ActiveEnd != overview.Totals.Active {
		t.Fatalf("active timeline tail end = %d, active total = %d, tail=%+v", overview.Timeline[len(overview.Timeline)-1].ActiveEnd, overview.Totals.Active, overview.Timeline[len(overview.Timeline)-1])
	}
}

func TestReconcileSessionSnapshotHidesMissingActiveSessionWithoutSyntheticEvent(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetDashboardConfig(DashboardConfig{ActiveSessionMaxAge: 0})

	now := time.Now().UTC()
	seedDashboardEnrichedForClient(t, st, "live-open", "client-live", "connected", "matched", "wss", "edge-1", "live.host", "198.51.100.10", "203.0.113.10", "edge1.example.com", now.Add(-30*time.Minute))
	seedDashboardEnrichedForClient(t, st, "ghost-open", "client-ghost", "connected", "matched", "wss", "edge-1", "ghost.host", "198.51.100.11", "203.0.113.10", "edge1.example.com", now.Add(-25*time.Minute))

	result, err := st.ReconcileSessionSnapshot([]string{"client-live"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.ActiveBefore != 2 || result.Live != 1 || result.Closed != 0 || len(result.ClosedReverseSSHIDs) != 0 || result.SnapshotID == 0 {
		t.Fatalf("unexpected snapshot result: %+v", result)
	}
	second, err := st.ReconcileSessionSnapshot([]string{"client-live"}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.Closed != 0 {
		t.Fatalf("second snapshot created duplicate close: %+v", second)
	}

	overview, err := st.DashboardOverview(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Totals.Active != 1 || len(overview.ActiveSessions) != 1 || overview.ActiveSessions[0].ReverseSSHID != "client-live" {
		t.Fatalf("active sessions after reconciliation = %+v totals=%+v", overview.ActiveSessions, overview.Totals)
	}
	events, err := st.DashboardEvents(context.Background(), DashboardEventQuery{Window: 24 * time.Hour, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Synthetic || events[0].IngestSource == "reconciler" {
		t.Fatalf("snapshot created synthetic recent event: %+v", events)
	}
	var syntheticCount int
	if err := st.db.QueryRow(`SELECT count(*) FROM enriched_events WHERE reverse_ssh_id = ? AND ingest_source = 'reconciler'`, "client-ghost").Scan(&syntheticCount); err != nil {
		t.Fatal(err)
	}
	if syntheticCount != 0 {
		t.Fatalf("synthetic close count = %d", syntheticCount)
	}
}

func TestDashboardActiveSessionsUseLiveSnapshotOverSyntheticDisconnect(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetDashboardConfig(DashboardConfig{ActiveSessionMaxAge: 0})

	now := time.Now().UTC()
	seedDashboardEnrichedForClient(t, st, "live-open", "client-live", "connected", "matched", "wss", "edge-1", "live.host", "198.51.100.10", "203.0.113.10", "edge1.example.com", now.Add(-30*time.Minute))
	seedDashboardEnrichedForClient(t, st, "fake-close", "client-live", "disconnected", "matched", "wss", "edge-1", "live.host", "198.51.100.10", "203.0.113.10", "edge1.example.com", now.Add(-5*time.Minute))
	if _, err := st.db.Exec(`
UPDATE enriched_events
SET ingest_source = 'reconciler', ingest_reason = 'missing_from_live_snapshot', synthetic = 1
WHERE event_hash = ?`, "dashboard-event-fake-close"); err != nil {
		t.Fatal(err)
	}

	if _, err := st.ReconcileSessionSnapshot([]string{"client-live"}, now); err != nil {
		t.Fatal(err)
	}
	overview, err := st.DashboardOverview(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Totals.Active != 1 || len(overview.ActiveSessions) != 1 {
		t.Fatalf("active sessions = %+v totals=%+v", overview.ActiveSessions, overview.Totals)
	}
	if event := overview.ActiveSessions[0]; event.ReverseSSHID != "client-live" || event.Status != "connected" || event.IngestSource == "reconciler" {
		t.Fatalf("unexpected active session from snapshot: %+v", event)
	}
}

func TestDashboardOverviewExpiresStaleActiveSessions(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetDashboardConfig(DashboardConfig{ActiveSessionMaxAge: 30 * time.Minute})

	now := time.Now().UTC()
	seedDashboardEnrichedForClient(t, st, "stale-open", "client-stale", "connected", "matched", "https", "edge-1", "stale.host", "198.51.100.40", "203.0.113.10", "edge1.example.com", now.Add(-45*time.Minute))
	seedDashboardEnrichedForClient(t, st, "fresh-open", "client-fresh", "connected", "matched", "https", "edge-1", "fresh.host", "198.51.100.41", "203.0.113.10", "edge1.example.com", now.Add(-10*time.Minute))

	overview, err := st.DashboardOverview(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Totals.Active != 1 {
		t.Fatalf("active total = %d, totals=%+v", overview.Totals.Active, overview.Totals)
	}
	if len(overview.ActiveSessions) != 1 || overview.ActiveSessions[0].ReverseSSHID != "client-fresh" {
		t.Fatalf("active sessions = %+v", overview.ActiveSessions)
	}
	if tail := overview.Timeline[len(overview.Timeline)-1]; tail.ActiveEnd != 1 {
		t.Fatalf("active timeline tail end = %d, tail=%+v", tail.ActiveEnd, tail)
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

func TestDashboardTimelineSeparatesPeakFromBucketEnd(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetDashboardConfig(DashboardConfig{ActiveSessionMaxAge: 24 * time.Hour})

	until := time.Date(2026, 7, 1, 15, 45, 0, 0, time.UTC)
	seedDashboardEnrichedForClient(t, st, "active-one", "client-one", "connected", "matched", "wss", "edge-1", "one.host", "198.51.100.30", "203.0.113.10", "edge1.example.com", until.Add(-3*time.Hour))
	seedDashboardEnrichedForClient(t, st, "active-two", "client-two", "connected", "matched", "wss", "edge-1", "two.host", "198.51.100.31", "203.0.113.10", "edge1.example.com", until.Add(-2*time.Hour))
	seedDashboardEnrichedForClient(t, st, "short-open", "client-short", "connected", "matched", "wss", "edge-1", "short.host", "198.51.100.32", "203.0.113.10", "edge1.example.com", until.Add(-30*time.Minute))
	seedDashboardEnrichedForClient(t, st, "short-close", "client-short", "disconnected", "matched", "wss", "edge-1", "short.host", "198.51.100.32", "203.0.113.10", "edge1.example.com", until.Add(-10*time.Minute))

	timeline, err := st.dashboardTimeline(context.Background(), dashboardTimeBounds{
		window: 24 * time.Hour,
		since:  until.Add(-24 * time.Hour).Format(time.RFC3339Nano),
		until:  until.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	last := timeline[len(timeline)-1]
	if last.Active != 3 {
		t.Fatalf("last bucket peak active = %d, bucket=%+v", last.Active, last)
	}
	if last.ActiveEnd != 2 {
		t.Fatalf("last bucket active end = %d, bucket=%+v", last.ActiveEnd, last)
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

func TestDashboardSystemEventsReturnsIngressAndReverseSSHErrors(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now().UTC()
	ingress, err := events.NormalizeIngressEvent(events.IngressEvent{
		Transport:       "wss",
		VPSName:         "edge-1",
		VPSPublicIP:     "203.0.113.10",
		VPSInternalIP:   "10.0.0.10",
		ClientIP:        "198.51.100.10",
		ClientPort:      53000,
		Host:            "edge1.example.com",
		URI:             "/ws",
		Method:          "GET",
		Upgrade:         "websocket",
		NginxReceivedAt: now.Add(-2 * time.Minute),
	}, now.Add(-2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if inserted, err := st.InsertIngressEvent(ingress); err != nil || !inserted {
		t.Fatalf("insert ingress inserted=%v err=%v", inserted, err)
	}

	errorEvent, err := events.NormalizeReverseSSHErrorEvent(events.ReverseSSHErrorEvent{
		Source:     "journalctl",
		Unit:       "reverse_ssh",
		Message:    "public key fingerprint mismatch from 198.51.100.99:53001",
		RemoteAddr: "198.51.100.99:53001",
		ObservedAt: now.Add(-time.Minute),
	}, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if inserted, err := st.InsertReverseSSHErrorEvent(errorEvent); err != nil || !inserted {
		t.Fatalf("insert error inserted=%v err=%v", inserted, err)
	}

	systemEvents, err := st.DashboardSystemEvents(context.Background(), DashboardSystemEventQuery{
		Window: 24 * time.Hour,
		Limit:  10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(systemEvents) != 2 {
		t.Fatalf("system events length = %d, events=%+v", len(systemEvents), systemEvents)
	}
	if systemEvents[0].Kind != "reverse_ssh_error" || systemEvents[0].Reason != "fingerprint_mismatch" {
		t.Fatalf("newest system event = %+v", systemEvents[0])
	}
	if systemEvents[1].Kind != "ingress" || systemEvents[1].Host != "edge1.example.com" {
		t.Fatalf("ingress system event = %+v", systemEvents[1])
	}

	filtered, err := st.DashboardSystemEvents(context.Background(), DashboardSystemEventQuery{
		Window:   24 * time.Hour,
		Severity: "error",
		Search:   "fingerprint",
		Limit:    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Kind != "reverse_ssh_error" {
		t.Fatalf("filtered system events = %+v", filtered)
	}

	withoutInfo, err := st.DashboardSystemEvents(context.Background(), DashboardSystemEventQuery{
		Window:   24 * time.Hour,
		Severity: "not_info",
		Limit:    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutInfo) != 1 || withoutInfo[0].Kind != "reverse_ssh_error" {
		t.Fatalf("non-info system events = %+v", withoutInfo)
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
