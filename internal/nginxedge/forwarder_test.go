package nginxedge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseLineCapturesWSSHandshake(t *testing.T) {
	forwarder := New(Config{
		VPSName:       "vps-1",
		VPSPublicIP:   "203.0.113.20",
		VPSInternalIP: "192.0.2.2",
		WSPath:        "/secret-ws",
		PushPath:      "/secret-push",
	})

	line := []byte(`{"ts":"2026-06-09T12:00:00Z","request_id":"req-1","remote_addr":"198.51.100.10","remote_port":"5555","host":"entry.example.com","request_method":"GET","request_uri":"/secret-ws","uri":"/secret-ws","http_upgrade":"websocket","http_connection":"Upgrade","http_user_agent":"test","server_addr":"192.0.2.2"}`)
	event, ok, err := forwarder.ParseLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected WSS line to be captured")
	}
	if event.Transport != "wss" || event.URI != "/secret-ws" || event.ClientIP != "198.51.100.10" {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestParseLineCapturesHTTPSInitAndHashesKey(t *testing.T) {
	forwarder := New(Config{
		VPSName:       "vps-1",
		VPSInternalIP: "192.0.2.2",
		PushPath:      "/secret-push",
	})

	line := []byte(`{"ts":"2026-06-09T12:00:00Z","remote_addr":"198.51.100.10","remote_port":"5555","request_method":"HEAD","request_uri":"/secret-push?key=abcdef","uri":"/secret-push","server_addr":"192.0.2.2"}`)
	event, ok, err := forwarder.ParseLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected HTTPS init line to be captured")
	}
	if event.Transport != "https" || event.PollingKeySHA1 == "" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if strings.Contains(event.PollingKeySHA1, "abcdef") {
		t.Fatal("polling key was not hashed")
	}
}

func TestParseLineIgnoresHTTPSPollingNoise(t *testing.T) {
	forwarder := New(Config{VPSName: "vps-1", PushPath: "/push"})
	for _, line := range [][]byte{
		[]byte(`{"remote_addr":"198.51.100.10","request_method":"HEAD","request_uri":"/push","uri":"/push"}`),
		[]byte(`{"remote_addr":"198.51.100.10","request_method":"GET","request_uri":"/push/123?id=x","uri":"/push/123"}`),
		[]byte(`{"remote_addr":"198.51.100.10","request_method":"POST","request_uri":"/push?id=x","uri":"/push"}`),
	} {
		_, ok, err := forwarder.ParseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatalf("expected polling line to be ignored: %s", string(line))
		}
	}
}

func TestParseLineRejectsMismatchedExplicitTransport(t *testing.T) {
	forwarder := New(Config{VPSName: "vps-1", PushPath: "/push"})
	_, ok, err := forwarder.ParseLine([]byte(`{"transport":"https","remote_addr":"198.51.100.10","request_method":"GET","request_uri":"/push/123?id=x","uri":"/push/123"}`))
	if err == nil {
		t.Fatal("expected mismatched explicit transport to fail")
	}
	if ok {
		t.Fatal("mismatched transport should not be captured")
	}
}

func TestCaptureHandlerSpoolsMirrorRequest(t *testing.T) {
	dir := t.TempDir()
	forwarder := New(Config{
		SpoolDir:      dir,
		VPSName:       "vps-1",
		VPSInternalIP: "192.0.2.2",
	})
	req := httptest.NewRequest(http.MethodGet, "/capture", nil)
	req.Header.Set("X-RSSH-Transport", "wss")
	req.Header.Set("X-Real-IP", "198.51.100.10")
	req.Header.Set("X-Original-Remote-Port", "5555")
	req.Header.Set("X-Original-Method", "GET")
	req.Header.Set("X-Original-URI", "/ws")
	req.Header.Set("X-Original-Path", "/ws")
	req.Header.Set("X-Original-Upgrade", "websocket")
	req.Header.Set("X-Original-Server-Addr", "192.0.2.2")
	rec := httptest.NewRecorder()

	forwarder.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			found = true
		}
	}
	if !found {
		t.Fatal("capture request did not create spool file")
	}
}

func TestSpoolAndFlush(t *testing.T) {
	var received bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingress-events/secret" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		received = true
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	dir := t.TempDir()
	forwarder := New(Config{
		SpoolDir:      dir,
		ForwardURL:    server.URL + "/ingress-events",
		ForwardToken:  "secret",
		VPSName:       "vps-1",
		VPSInternalIP: "192.0.2.2",
	})
	event, ok, err := forwarder.ParseLine([]byte(`{"ts":"2026-06-09T12:00:00Z","remote_addr":"198.51.100.10","request_method":"GET","request_uri":"/ws","uri":"/ws","http_upgrade":"websocket","server_addr":"192.0.2.2"}`))
	if err != nil || !ok {
		t.Fatalf("parse ok=%v err=%v", ok, err)
	}
	if err := forwarder.SpoolEvent(event); err != nil {
		t.Fatal(err)
	}
	if err := forwarder.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !received {
		t.Fatal("server did not receive event")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			t.Fatalf("spool file was not removed: %s", entry.Name())
		}
	}
}

func TestFlushLeavesSpoolOnFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	dir := t.TempDir()
	forwarder := New(Config{
		SpoolDir:      dir,
		ForwardURL:    server.URL + "/ingress-events",
		ForwardToken:  "secret",
		VPSName:       "vps-1",
		VPSInternalIP: "192.0.2.2",
		HTTPTimeout:   time.Second,
	})
	event, ok, err := forwarder.ParseLine([]byte(`{"ts":"2026-06-09T12:00:00Z","remote_addr":"198.51.100.10","request_method":"GET","request_uri":"/ws","uri":"/ws","http_upgrade":"websocket","server_addr":"192.0.2.2"}`))
	if err != nil || !ok {
		t.Fatalf("parse ok=%v err=%v", ok, err)
	}
	if err := forwarder.SpoolEvent(event); err != nil {
		t.Fatal(err)
	}
	if err := forwarder.Flush(context.Background()); err == nil {
		t.Fatal("expected flush failure")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			found = true
		}
	}
	if !found {
		t.Fatal("spool file was removed after failed flush")
	}
}
