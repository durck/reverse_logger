package loggerapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/durck/reverse_logger/internal/events"
	"github.com/durck/reverse_logger/internal/store"
	"github.com/durck/reverse_logger/internal/telegram"
)

func setTelegramConnectedAlertDelay(t *testing.T, delay time.Duration) {
	t.Helper()
	previous := telegramConnectedAlertEnrichmentDelay
	telegramConnectedAlertEnrichmentDelay = delay
	t.Cleanup(func() {
		telegramConnectedAlertEnrichmentDelay = previous
	})
}

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

func TestWebhookRetrySendsTelegramAfterEnrichmentFailure(t *testing.T) {
	setTelegramConnectedAlertDelay(t, time.Millisecond)

	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sent := make(chan string, 1)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		sent <- r.Form.Get("chat_id")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer api.Close()

	tg, err := telegram.New(telegram.Config{
		Enabled:  true,
		BotToken: "token",
		ChatIDs:  []string{"123"},
		APIBase:  api.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer("secret", "edge-secret", st, tg)

	enrichedPath := filepath.Join(dir, "enriched_events.jsonl")
	if err := os.Mkdir(enrichedPath, 0o750); err != nil {
		t.Fatal(err)
	}
	body := `{"Status":"connected","ID":"abc","IP":"192.0.2.1:5555","HostName":"u.c","Timestamp":"2026-06-09T12:00:00Z"}`

	req := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(body))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case chatID := <-sent:
		t.Fatalf("unexpected alert after failed enrichment to chat %q", chatID)
	default:
	}

	if err := os.Remove(enrichedPath); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(body))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d body=%s", rec.Code, rec.Body.String())
	}

	select {
	case chatID := <-sent:
		if chatID != "123" {
			t.Fatalf("alert chat_id = %q", chatID)
		}
	case <-time.After(time.Second):
		t.Fatal("telegram alert was not sent after successful retry enrichment")
	}
	time.Sleep(50 * time.Millisecond)
}

func TestWebhookTelegramAlertWaitsForIngressCorrelation(t *testing.T) {
	setTelegramConnectedAlertDelay(t, 100*time.Millisecond)

	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sent := make(chan string, 1)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		sent <- r.Form.Get("text")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":77}}`))
	}))
	defer api.Close()

	tg, err := telegram.New(telegram.Config{
		Enabled:  true,
		BotToken: "token",
		ChatIDs:  []string{"123"},
		APIBase:  api.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer("secret", "edge-secret", st, tg)

	webhookBody := `{"Status":"connected","ID":"abc","IP":"192.0.2.2:4444","HostName":"u.c","Timestamp":"2026-06-09T12:00:05Z","Transport":"wss"}`
	webhookReq := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(webhookBody))
	webhookRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(webhookRec, webhookReq)
	if webhookRec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", webhookRec.Code, webhookRec.Body.String())
	}
	if !strings.Contains(webhookRec.Body.String(), `"correlation_status":"unmatched"`) {
		t.Fatalf("expected initial unmatched response: %s", webhookRec.Body.String())
	}

	ingressBody := `{"transport":"wss","vps_name":"vps-1","vps_internal_ip":"192.0.2.2","client_ip":"198.51.100.10","client_port":5555,"uri":"/ws","method":"GET","upgrade":"websocket","nginx_received_at":"2026-06-09T12:00:00Z"}`
	ingressReq := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(ingressBody))
	ingressRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(ingressRec, ingressReq)
	if ingressRec.Code != http.StatusAccepted {
		t.Fatalf("ingress status = %d body=%s", ingressRec.Code, ingressRec.Body.String())
	}
	if !strings.Contains(ingressRec.Body.String(), `"reconciled":1`) {
		t.Fatalf("expected one reconciled event: %s", ingressRec.Body.String())
	}

	select {
	case text := <-sent:
		for _, want := range []string{"real_ip", "198.51.100.10:5555", "via", "wss / vps-1"} {
			if !strings.Contains(text, want) {
				t.Fatalf("telegram text missing %q: %s", want, text)
			}
		}
		if strings.Contains(text, "correlation: unmatched") {
			t.Fatalf("telegram text used stale unmatched enrichment: %s", text)
		}
	case <-time.After(time.Second):
		t.Fatal("telegram alert was not sent")
	}
}

func TestNotifyTelegramEventSkipsSentDelivery(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sent := make(chan string, 2)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		sent <- r.Form.Get("text")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":77}}`))
	}))
	defer api.Close()

	tg, err := telegram.New(telegram.Config{
		Enabled:  true,
		BotToken: "token",
		ChatIDs:  []string{"123"},
		APIBase:  api.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer("secret", "edge-secret", st, tg)

	event, err := events.ParseWebhookPayload([]byte(`{"Status":"connected","ID":"abc","IP":"192.0.2.1:5555","HostName":"u.c","Timestamp":"2026-06-09T12:00:00Z"}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if inserted, err := st.InsertEvent(event); err != nil || !inserted {
		t.Fatalf("inserted=%v err=%v", inserted, err)
	}

	if err := server.notifyTelegramEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-sent:
		if !strings.Contains(text, "alert_id: "+telegram.AlertID(event)) {
			t.Fatalf("telegram text missing alert_id: %s", text)
		}
	case <-time.After(time.Second):
		t.Fatal("telegram alert was not sent")
	}

	if err := server.notifyTelegramEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-sent:
		t.Fatalf("duplicate telegram alert sent: %s", text)
	default:
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

func TestSourceIPEndpointReportsRemoteAddr(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/edge/source-ip/edge-secret", nil)
	req.RemoteAddr = "192.0.2.44:53000"
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		SourceIP   string `json:"source_ip"`
		RemoteAddr string `json:"remote_addr"`
		SeenAt     string `json:"seen_at"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.SourceIP != "192.0.2.44" || response.RemoteAddr != "192.0.2.44:53000" || response.SeenAt == "" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestSourceIPEndpointAcceptsBearerToken(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/edge/source-ip", nil)
	req.RemoteAddr = "[2001:db8::10]:53000"
	req.Header.Set("Authorization", "Bearer edge-secret")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		SourceIP string `json:"source_ip"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.SourceIP != "2001:db8::10" {
		t.Fatalf("source_ip = %q", response.SourceIP)
	}
}

func TestSourceIPEndpointRejectsWrongToken(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/edge/source-ip/wrong", nil)
	req.RemoteAddr = "192.0.2.44:53000"
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
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

func TestIngressEventEndpointStoresEvent(t *testing.T) {
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

	body := `{"transport":"wss","vps_name":"vps-1","vps_internal_ip":"192.0.2.2","client_ip":"198.51.100.10","client_port":5555,"uri":"/ws","method":"GET","upgrade":"websocket","nginx_received_at":"2026-06-09T12:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(body))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "ingress_events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"transport":"wss"`) {
		t.Fatalf("ingress jsonl missing event: %s", string(content))
	}
}

func TestReverseSSHErrorEndpointStoresEventAndSendsTelegram(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sent := make(chan string, 2)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		sent <- r.Form.Get("text")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
	}))
	defer api.Close()

	tg, err := telegram.New(telegram.Config{
		Enabled:  true,
		BotToken: "token",
		ChatIDs:  []string{"123"},
		APIBase:  api.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer("secret", "edge-secret", st, tg)

	body := `{"source":"journalctl","unit":"reverse_ssh","message":"public key fingerprint mismatch from 198.51.100.10:53000","remote_addr":"198.51.100.10:53000","observed_at":"2026-07-08T12:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/reverse-ssh-errors/edge-secret", strings.NewReader(body))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"reason":"fingerprint_mismatch"`) {
		t.Fatalf("response missing reason: %s", rec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "reverse_ssh_errors.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "fingerprint_mismatch") {
		t.Fatalf("reverse_ssh error jsonl missing event: %s", string(content))
	}
	select {
	case text := <-sent:
		if !strings.Contains(text, "reverse_ssh ERROR") || !strings.Contains(text, "fingerprint_mismatch") {
			t.Fatalf("unexpected telegram message: %s", text)
		}
	case <-time.After(time.Second):
		t.Fatal("telegram alert was not sent")
	}

	req = httptest.NewRequest(http.MethodPost, "/reverse-ssh-errors/edge-secret", strings.NewReader(body))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("duplicate status = %d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case text := <-sent:
		t.Fatalf("duplicate telegram alert sent: %s", text)
	default:
	}
}

func TestIngressEventEndpointRejectsWrongToken(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodPost, "/ingress-events/wrong", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestIngressEventEndpointRejectsMalformedHTTPSShape(t *testing.T) {
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

	tests := []string{
		`{"transport":"https","vps_name":"vps-1","vps_internal_ip":"192.0.2.2","client_ip":"198.51.100.10","uri":"/push","method":"HEAD","nginx_received_at":"2026-06-09T12:00:00Z"}`,
		`{"transport":"https","vps_name":"vps-1","vps_internal_ip":"192.0.2.2","client_ip":"198.51.100.10","uri":"/push","method":"HEAD","polling_key_sha1":"not-a-sha1","nginx_received_at":"2026-06-09T12:00:00Z"}`,
		`{"transport":"https","vps_name":"vps-1","vps_internal_ip":"192.0.2.2","client_ip":"198.51.100.10","uri":"/anything","method":"HEAD","polling_key_sha1":"a9993e364706816aba3e25717850c26c9cd0d89d","nginx_received_at":"2026-06-09T12:00:00Z"}`,
		`{"transport":"wss","vps_name":"vps-1","vps_internal_ip":"192.0.2.2","client_ip":"198.51.100.10","uri":"/anything","method":"GET","upgrade":"websocket","nginx_received_at":"2026-06-09T12:00:00Z"}`,
	}
	for _, body := range tests {
		req := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(body))
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d body=%s payload=%s", rec.Code, rec.Body.String(), body)
		}
	}
}

func TestWebhookCreatesMatchedEnrichedEvent(t *testing.T) {
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

	ingressBody := `{"transport":"https","vps_name":"vps-1","vps_public_ip":"203.0.113.20","vps_internal_ip":"192.0.2.2","client_ip":"198.51.100.10","client_port":5555,"uri":"/push","method":"HEAD","polling_key_sha1":"a9993e364706816aba3e25717850c26c9cd0d89d","nginx_received_at":"2026-06-09T12:00:00Z"}`
	ingressReq := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(ingressBody))
	ingressRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(ingressRec, ingressReq)
	if ingressRec.Code != http.StatusAccepted {
		t.Fatalf("ingress status = %d body=%s", ingressRec.Code, ingressRec.Body.String())
	}

	webhookBody := `{"Status":"connected","ID":"abc","IP":"192.0.2.2:4444","HostName":"u.c","Timestamp":"2026-06-09T12:00:05Z"}`
	webhookReq := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(webhookBody))
	webhookRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(webhookRec, webhookReq)
	if webhookRec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", webhookRec.Code, webhookRec.Body.String())
	}
	if !strings.Contains(webhookRec.Body.String(), `"correlation_status":"matched"`) {
		t.Fatalf("webhook response did not report matched correlation: %s", webhookRec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "enriched_events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"real_client_ip":"198.51.100.10"`, `"transport":"https"`, `"correlation_status":"matched"`} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("enriched jsonl missing %s: %s", want, string(content))
		}
	}
}

func TestWebhookMatchesDelayedHTTPSIngressEvent(t *testing.T) {
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

	ingressBody := `{"transport":"https","vps_name":"vps-1","vps_public_ip":"203.0.113.20","vps_internal_ip":"192.0.2.2","client_ip":"198.51.100.10","client_port":5555,"uri":"/push","method":"HEAD","polling_key_sha1":"a9993e364706816aba3e25717850c26c9cd0d89d","nginx_received_at":"2026-06-09T12:00:00Z"}`
	ingressReq := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(ingressBody))
	ingressRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(ingressRec, ingressReq)
	if ingressRec.Code != http.StatusAccepted {
		t.Fatalf("ingress status = %d body=%s", ingressRec.Code, ingressRec.Body.String())
	}

	webhookBody := `{"Status":"connected","ID":"abc","IP":"192.0.2.2:4444","HostName":"u.c","Timestamp":"2026-06-09T12:03:00Z","Transport":"https"}`
	webhookReq := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(webhookBody))
	webhookRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(webhookRec, webhookReq)
	if webhookRec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", webhookRec.Code, webhookRec.Body.String())
	}
	if !strings.Contains(webhookRec.Body.String(), `"correlation_status":"matched"`) {
		t.Fatalf("webhook response did not report matched correlation: %s", webhookRec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "enriched_events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"real_client_ip":"198.51.100.10"`, `"transport":"https"`, `"correlation_status":"matched"`} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("enriched jsonl missing %s: %s", want, string(content))
		}
	}
}

func TestWebhookCreatesUnmatchedEnrichedEvent(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(`{"Status":"connected","ID":"abc","IP":"192.0.2.2:4444","HostName":"u.c","Timestamp":"2026-06-09T12:00:05Z"}`))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "enriched_events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"correlation_status":"unmatched"`) {
		t.Fatalf("enriched jsonl missing unmatched event: %s", string(content))
	}
}

func TestIngressReconcilesEarlierUnmatchedWebhook(t *testing.T) {
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

	webhookBody := `{"Status":"connected","ID":"abc","IP":"192.0.2.2:4444","HostName":"u.c","Timestamp":"2026-06-09T12:00:05Z"}`
	webhookReq := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(webhookBody))
	webhookRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(webhookRec, webhookReq)
	if webhookRec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", webhookRec.Code, webhookRec.Body.String())
	}
	if !strings.Contains(webhookRec.Body.String(), `"correlation_status":"unmatched"`) {
		t.Fatalf("expected initial unmatched response: %s", webhookRec.Body.String())
	}

	ingressBody := `{"transport":"wss","vps_name":"vps-1","vps_internal_ip":"192.0.2.2","client_ip":"198.51.100.10","client_port":5555,"uri":"/ws","method":"GET","upgrade":"websocket","nginx_received_at":"2026-06-09T12:00:00Z"}`
	ingressReq := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(ingressBody))
	ingressRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(ingressRec, ingressReq)
	if ingressRec.Code != http.StatusAccepted {
		t.Fatalf("ingress status = %d body=%s", ingressRec.Code, ingressRec.Body.String())
	}
	if !strings.Contains(ingressRec.Body.String(), `"reconciled":1`) {
		t.Fatalf("expected one reconciled event: %s", ingressRec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "enriched_events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"correlation_status":"unmatched"`, `"correlation_status":"matched"`, `"real_client_ip":"198.51.100.10"`} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("enriched jsonl missing %s: %s", want, string(content))
		}
	}
}

func TestIngressReconcilesTrustedProxyWebhookByProxySourceIP(t *testing.T) {
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

	webhookBody := `{"Status":"connected","ID":"abc","IP":"198.51.100.10:0","HostName":"u.c","Timestamp":"2026-06-09T12:00:05Z","Transport":"wss","PublicKeyFingerprint":"fp","ProxySourceIP":"192.0.2.2"}`
	webhookReq := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(webhookBody))
	webhookRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(webhookRec, webhookReq)
	if webhookRec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", webhookRec.Code, webhookRec.Body.String())
	}
	if !strings.Contains(webhookRec.Body.String(), `"correlation_status":"unmatched"`) {
		t.Fatalf("expected initial unmatched response: %s", webhookRec.Body.String())
	}

	ingressBody := `{"transport":"wss","vps_name":"vps-1","vps_internal_ip":"192.0.2.2","client_ip":"198.51.100.10","client_port":5555,"uri":"/ws","method":"GET","upgrade":"websocket","nginx_received_at":"2026-06-09T12:00:00Z"}`
	ingressReq := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(ingressBody))
	ingressRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(ingressRec, ingressReq)
	if ingressRec.Code != http.StatusAccepted {
		t.Fatalf("ingress status = %d body=%s", ingressRec.Code, ingressRec.Body.String())
	}
	if !strings.Contains(ingressRec.Body.String(), `"reconciled":1`) {
		t.Fatalf("expected one reconciled event: %s", ingressRec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "enriched_events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"real_client_ip":"198.51.100.10"`, `"proxy_source_ip":"192.0.2.2"`, `"public_key_fingerprint":"fp"`} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("enriched jsonl missing %s: %s", want, string(content))
		}
	}
}

func TestTrustedProxyWebhookDoesNotMatchDifferentIngressClientIP(t *testing.T) {
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

	ingressBody := `{"transport":"wss","vps_name":"vps-1","vps_internal_ip":"192.0.2.2","client_ip":"198.51.100.99","client_port":5555,"uri":"/ws","method":"GET","upgrade":"websocket","nginx_received_at":"2026-06-09T12:00:00Z"}`
	ingressReq := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(ingressBody))
	ingressRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(ingressRec, ingressReq)
	if ingressRec.Code != http.StatusAccepted {
		t.Fatalf("ingress status = %d body=%s", ingressRec.Code, ingressRec.Body.String())
	}

	webhookBody := `{"Status":"connected","ID":"abc","IP":"198.51.100.10:0","HostName":"u.c","Timestamp":"2026-06-09T12:00:05Z","Transport":"wss","ProxySourceIP":"192.0.2.2"}`
	webhookReq := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(webhookBody))
	webhookRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(webhookRec, webhookReq)
	if webhookRec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", webhookRec.Code, webhookRec.Body.String())
	}
	if !strings.Contains(webhookRec.Body.String(), `"correlation_status":"unmatched"`) {
		t.Fatalf("expected unmatched correlation: %s", webhookRec.Body.String())
	}
}

func TestWebhookMatchesWhenVPSInternalIPMissingButForwarderIPObserved(t *testing.T) {
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

	ingressBody := `{"transport":"wss","vps_name":"vps-1","client_ip":"198.51.100.10","client_port":5555,"uri":"/ws","method":"GET","upgrade":"websocket","nginx_received_at":"2026-06-09T12:00:00Z"}`
	ingressReq := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(ingressBody))
	ingressReq.RemoteAddr = "192.0.2.2:53000"
	ingressRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(ingressRec, ingressReq)
	if ingressRec.Code != http.StatusAccepted {
		t.Fatalf("ingress status = %d body=%s", ingressRec.Code, ingressRec.Body.String())
	}

	webhookBody := `{"Status":"connected","ID":"abc","IP":"192.0.2.2:4444","HostName":"u.c","Timestamp":"2026-06-09T12:00:05Z"}`
	webhookReq := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(webhookBody))
	webhookRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(webhookRec, webhookReq)
	if webhookRec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", webhookRec.Code, webhookRec.Body.String())
	}
	if !strings.Contains(webhookRec.Body.String(), `"correlation_status":"matched"`) {
		t.Fatalf("expected matched correlation: %s", webhookRec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "enriched_events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"forwarder_ip":"192.0.2.2"`, `"correlation_method":"vps-or-forwarder-ip"`} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("enriched jsonl missing %s: %s", want, string(content))
		}
	}
}

func TestWebhookMatchesWhenVPSInternalIPWrongButForwarderIPObserved(t *testing.T) {
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

	ingressBody := `{"transport":"wss","vps_name":"vps-1","vps_internal_ip":"10.0.0.99","client_ip":"198.51.100.10","client_port":5555,"uri":"/ws","method":"GET","upgrade":"websocket","nginx_received_at":"2026-06-09T12:00:00Z"}`
	ingressReq := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(ingressBody))
	ingressReq.RemoteAddr = "192.0.2.2:53000"
	ingressRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(ingressRec, ingressReq)
	if ingressRec.Code != http.StatusAccepted {
		t.Fatalf("ingress status = %d body=%s", ingressRec.Code, ingressRec.Body.String())
	}

	webhookBody := `{"Status":"connected","ID":"abc","IP":"192.0.2.2:4444","HostName":"u.c","Timestamp":"2026-06-09T12:00:05Z"}`
	webhookReq := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(webhookBody))
	webhookRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(webhookRec, webhookReq)
	if webhookRec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", webhookRec.Code, webhookRec.Body.String())
	}
	if !strings.Contains(webhookRec.Body.String(), `"correlation_status":"matched"`) {
		t.Fatalf("expected matched correlation through observed forwarder IP: %s", webhookRec.Body.String())
	}
}

func TestWebhookMatchesPublicInternetHTTPSIngressByForwarderIP(t *testing.T) {
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

	ingressBody := `{"transport":"https","vps_name":"edge-public","vps_public_ip":"201.51.5.226","client_ip":"212.73.99.133","client_port":5555,"uri":"/push?key=abcdef","method":"HEAD","polling_key_sha1":"1f8ac10f23c5b5bc1167bda84b833e5c057a77d2","nginx_received_at":"2026-07-08T18:54:44Z"}`
	ingressReq := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(ingressBody))
	ingressReq.RemoteAddr = "201.51.5.226:53000"
	ingressRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(ingressRec, ingressReq)
	if ingressRec.Code != http.StatusAccepted {
		t.Fatalf("ingress status = %d body=%s", ingressRec.Code, ingressRec.Body.String())
	}

	webhookBody := `{"Status":"connected","ID":"abc","IP":"201.51.5.226:443","HostName":"u.c","Timestamp":"2026-07-08T18:54:45Z","Transport":"https"}`
	webhookReq := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(webhookBody))
	webhookRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(webhookRec, webhookReq)
	if webhookRec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", webhookRec.Code, webhookRec.Body.String())
	}
	if !strings.Contains(webhookRec.Body.String(), `"correlation_status":"matched"`) {
		t.Fatalf("expected public redirector ingress match: %s", webhookRec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "enriched_events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"real_client_ip":"212.73.99.133"`, `"vps_public_ip":"201.51.5.226"`, `"forwarder_ip":"201.51.5.226"`} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("enriched jsonl missing %s: %s", want, string(content))
		}
	}
}

func TestTrustedProxyWebhookFallsBackToClientIPWhenVPSAddressWrong(t *testing.T) {
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

	ingressBody := `{"transport":"wss","vps_name":"vps-1","vps_internal_ip":"10.0.0.99","client_ip":"198.51.100.10","client_port":5555,"uri":"/ws","method":"GET","upgrade":"websocket","nginx_received_at":"2026-06-09T12:00:00Z"}`
	ingressReq := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(ingressBody))
	ingressReq.RemoteAddr = "10.0.0.99:53000"
	ingressRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(ingressRec, ingressReq)
	if ingressRec.Code != http.StatusAccepted {
		t.Fatalf("ingress status = %d body=%s", ingressRec.Code, ingressRec.Body.String())
	}

	webhookBody := `{"Status":"connected","ID":"abc","IP":"198.51.100.10:0","HostName":"u.c","Timestamp":"2026-06-09T12:00:05Z","Transport":"wss","ProxySourceIP":"192.0.2.2"}`
	webhookReq := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(webhookBody))
	webhookRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(webhookRec, webhookReq)
	if webhookRec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", webhookRec.Code, webhookRec.Body.String())
	}
	if !strings.Contains(webhookRec.Body.String(), `"correlation_status":"matched"`) {
		t.Fatalf("expected trusted-proxy client-ip fallback match: %s", webhookRec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "enriched_events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"correlation_method":"trusted-proxy-client-ip-fallback"`) {
		t.Fatalf("enriched jsonl missing client-ip fallback method: %s", string(content))
	}
}

func TestWebhookMatchesAcrossSourceAndNginxClockSkew(t *testing.T) {
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

	ingressBody := `{"transport":"wss","vps_name":"vps-1","vps_internal_ip":"192.0.2.2","client_ip":"198.51.100.10","client_port":5555,"uri":"/ws","method":"GET","upgrade":"websocket","nginx_received_at":"2001-01-01T00:00:00Z"}`
	ingressReq := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(ingressBody))
	ingressReq.RemoteAddr = "192.0.2.2:53000"
	ingressRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(ingressRec, ingressReq)
	if ingressRec.Code != http.StatusAccepted {
		t.Fatalf("ingress status = %d body=%s", ingressRec.Code, ingressRec.Body.String())
	}

	webhookBody := `{"Status":"connected","ID":"abc","IP":"192.0.2.2:4444","HostName":"u.c","Timestamp":"1999-01-01T00:00:00Z"}`
	webhookReq := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(webhookBody))
	webhookRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(webhookRec, webhookReq)
	if webhookRec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", webhookRec.Code, webhookRec.Body.String())
	}
	if !strings.Contains(webhookRec.Body.String(), `"correlation_status":"matched"`) {
		t.Fatalf("expected central receive times to bridge source/nginx clock skew: %s", webhookRec.Body.String())
	}
}

func TestWebhookMarksAmbiguousWhenMultipleIngressCandidates(t *testing.T) {
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

	for _, clientIP := range []string{"198.51.100.10", "198.51.100.11"} {
		body := strings.ReplaceAll(`{"transport":"wss","vps_name":"vps-1","vps_internal_ip":"192.0.2.2","client_ip":"CLIENT_IP","client_port":5555,"uri":"/ws","method":"GET","upgrade":"websocket","nginx_received_at":"2026-06-09T12:00:00Z"}`, "CLIENT_IP", clientIP)
		req := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(body))
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("ingress status = %d body=%s", rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(`{"Status":"connected","ID":"abc","IP":"192.0.2.2:4444","HostName":"u.c","Timestamp":"2026-06-09T12:00:05Z"}`))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"correlation_status":"ambiguous"`) {
		t.Fatalf("expected ambiguous correlation: %s", rec.Body.String())
	}
}

func TestLateIngressDoesNotOverwriteMatchedEnrichedEvent(t *testing.T) {
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

	firstIngress := `{"transport":"wss","vps_name":"vps-1","vps_internal_ip":"192.0.2.2","client_ip":"198.51.100.10","client_port":5555,"uri":"/ws","method":"GET","upgrade":"websocket","nginx_received_at":"2026-06-09T12:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(firstIngress))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first ingress status = %d body=%s", rec.Code, rec.Body.String())
	}

	webhook := `{"Status":"connected","ID":"abc","IP":"192.0.2.2:4444","HostName":"u.c","Timestamp":"2026-06-09T12:00:05Z"}`
	req = httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(webhook))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"correlation_status":"matched"`) {
		t.Fatalf("expected matched webhook: %s", rec.Body.String())
	}

	secondIngress := `{"transport":"wss","vps_name":"vps-1","vps_internal_ip":"192.0.2.2","client_ip":"198.51.100.99","client_port":5556,"uri":"/ws","method":"GET","upgrade":"websocket","nginx_received_at":"2026-06-09T12:00:06Z"}`
	req = httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(secondIngress))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("second ingress status = %d body=%s", rec.Code, rec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "enriched_events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), `"real_client_ip":"198.51.100.99"`) {
		t.Fatalf("late ingress overwrote matched event: %s", string(content))
	}
}

func TestDisconnectedDoesNotMatchFreshIngressWithoutPriorConnect(t *testing.T) {
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

	ingress := `{"transport":"wss","vps_name":"vps-1","vps_internal_ip":"192.0.2.2","client_ip":"198.51.100.10","client_port":5555,"uri":"/ws","method":"GET","upgrade":"websocket","nginx_received_at":"2026-06-09T12:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/ingress-events/edge-secret", strings.NewReader(ingress))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("ingress status = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/hooks/secret", strings.NewReader(`{"Status":"disconnected","ID":"abc","IP":"192.0.2.2:4444","HostName":"u.c","Timestamp":"2026-06-09T12:00:05Z"}`))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"correlation_status":"unmatched"`) {
		t.Fatalf("disconnect should stay unmatched without prior matched connect: %s", rec.Body.String())
	}
}

func TestDashboardDisabledWithoutToken(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDashboardRequiresAuth(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tg, err := telegram.New(telegram.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithDashboardToken("secret", "edge-secret", st, tg, "/ws", "/push", "dash-secret")

	req := httptest.NewRequest(http.MethodGet, "/dashboard/api/overview", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("missing Basic auth challenge: %q", rec.Header().Get("WWW-Authenticate"))
	}
}

func TestDashboardRejectsWrongToken(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tg, err := telegram.New(telegram.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithDashboardToken("secret", "edge-secret", st, tg, "/ws", "/push", "dash-secret")

	req := httptest.NewRequest(http.MethodGet, "/dashboard/api/overview", nil)
	req.Header.Set("Authorization", "Bearer wrong-secret")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDashboardAcceptsBasicAuthForPage(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tg, err := telegram.New(telegram.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithDashboardToken("secret", "edge-secret", st, tg, "/ws", "/push", "dash-secret")

	req := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	req.SetBasicAuth("operator", "dash-secret")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "reverse_ssh journal") {
		t.Fatalf("dashboard page body missing title")
	}
}

func TestDashboardAcceptsBasicAuthForHealthPage(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tg, err := telegram.New(telegram.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithDashboardToken("secret", "edge-secret", st, tg, "/ws", "/push", "dash-secret")

	req := httptest.NewRequest(http.MethodGet, "/dashboard/health", nil)
	req.SetBasicAuth("operator", "dash-secret")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "VPS health") {
		t.Fatalf("dashboard health page body missing health section")
	}
}

func TestDashboardAcceptsBasicAuthForConnectionsPage(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tg, err := telegram.New(telegram.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithDashboardToken("secret", "edge-secret", st, tg, "/ws", "/push", "dash-secret")

	req := httptest.NewRequest(http.MethodGet, "/dashboard/connections", nil)
	req.SetBasicAuth("operator", "dash-secret")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Connection events") {
		t.Fatalf("dashboard connections page body missing connection events section")
	}
}

func TestDashboardAcceptsBearerAuthForAPI(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tg, err := telegram.New(telegram.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithDashboardToken("secret", "edge-secret", st, tg, "/ws", "/push", "dash-secret")

	req := httptest.NewRequest(http.MethodGet, "/dashboard/api/overview?window=24h", nil)
	req.Header.Set("Authorization", "Bearer dash-secret")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Totals struct {
			Total int `json:"total"`
		} `json:"totals"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Totals.Total != 0 {
		t.Fatalf("unexpected total = %d", response.Totals.Total)
	}
}
