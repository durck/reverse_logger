package loggerapp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/durck/reverse_logger/internal/store"
	"github.com/durck/reverse_logger/internal/telegram"
)

func TestWebhookStoresEvent(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tg, err := telegram.New(telegram.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer("secret", "edge-secret", st, tg)

	req := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(`{"Status":"connected","ID":"abc","IP":"192.0.2.1:5555","HostName":"u.c","Timestamp":"2026-06-09T12:00:00Z"}`))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req.Clone(req.Context()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("reused request body should fail; got %d", rec.Code)
	}
}

func TestWebhookRejectsWrongToken(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tg, err := telegram.New(telegram.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer("secret", "", st, tg)
	req := httptest.NewRequest(http.MethodPost, "/hooks/wrong", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestWebhookRejectsEmptyEvent(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tg, err := telegram.New(telegram.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer("secret", "", st, tg)
	req := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEdgeEventEndpointStoresEvent(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tg, err := telegram.New(telegram.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer("secret", "edge-secret", st, tg)

	body := `{"event_hash":"hash-1","vps_name":"vps-1","vps_public_ip":"203.0.113.20","vps_port":3232,"client_ip":"198.51.100.10","client_port":5555,"received_at":"2026-06-09T12:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/edge-events/edge-secret", strings.NewReader(body))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "edge_events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "vps-1") {
		t.Fatalf("edge jsonl missing event: %s", string(content))
	}
}

func TestEdgeEventEndpointRecomputesClientHash(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tg, err := telegram.New(telegram.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer("secret", "edge-secret", st, tg)

	body := `{"event_hash":"spoofed","vps_name":"vps-1","vps_public_ip":"203.0.113.20","vps_port":3232,"client_ip":"198.51.100.10","client_port":5555,"received_at":"2026-06-09T12:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/edge-events/edge-secret", strings.NewReader(body))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		EventHash string `json:"event_hash"`
		Duplicate bool   `json:"duplicate"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.EventHash == "" || response.EventHash == "spoofed" {
		t.Fatalf("event_hash was not recomputed: %q", response.EventHash)
	}
	if response.Duplicate {
		t.Fatal("first event was reported duplicate")
	}

	req = httptest.NewRequest(http.MethodPost, "/edge-events/edge-secret", strings.NewReader(body))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("second status = %d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.Duplicate {
		t.Fatal("recomputed hash did not dedupe repeated edge event")
	}
}

func TestEdgeEventEndpointRejectsInvalidClientIP(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tg, err := telegram.New(telegram.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer("secret", "edge-secret", st, tg)

	body := `{"vps_name":"vps-1","client_ip":"not-an-ip","received_at":"2026-06-09T12:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/edge-events/edge-secret", strings.NewReader(body))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}
