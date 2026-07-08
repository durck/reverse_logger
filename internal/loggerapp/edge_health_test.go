package loggerapp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/durck/reverse_logger/internal/edgehealth"
	"github.com/durck/reverse_logger/internal/store"
	"github.com/durck/reverse_logger/internal/telegram"
)

func TestEdgeHealthEndpointStoresReport(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := newHealthTestServer(t, st, telegram.Config{})

	req := httptest.NewRequest(http.MethodPost, "/edge-health/health-secret", strings.NewReader(edgeHealthReportJSON(edgehealth.StatusOK)))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/dashboard/api/edge-health", nil)
	req.Header.Set("Authorization", "Bearer dash-secret")
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d body=%s", rec.Code, rec.Body.String())
	}
	var overview store.EdgeHealthOverview
	if err := json.NewDecoder(rec.Body).Decode(&overview); err != nil {
		t.Fatal(err)
	}
	if overview.Summary.OK != 1 || len(overview.Nodes) != 1 || overview.Nodes[0].VPSName != "vps-1" {
		t.Fatalf("unexpected overview: %+v", overview)
	}
}

func TestEdgeHealthEndpointRejectsWrongToken(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := newHealthTestServer(t, st, telegram.Config{})

	req := httptest.NewRequest(http.MethodPost, "/edge-health/wrong", strings.NewReader(edgeHealthReportJSON(edgehealth.StatusOK)))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEdgeHealthEndpointRejectsMalformedReport(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := newHealthTestServer(t, st, telegram.Config{})

	req := httptest.NewRequest(http.MethodPost, "/edge-health/health-secret", strings.NewReader(`{"vps_name":"vps-1","status":"ok","checks":[{"name":"logger","status":"bogus","required":true}]}`))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEdgeHealthExpectedEndpointRegistersNode(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := newHealthTestServer(t, st, telegram.Config{})

	req := httptest.NewRequest(http.MethodPut, "/edge-health/expected/health-secret", strings.NewReader(`{"nodes":[{"vps_name":"vps-1","vps_public_ip":"203.0.113.20"}],"bootstrap_grace_seconds":120}`))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/dashboard/api/edge-health", nil)
	req.SetBasicAuth("operator", "dash-secret")
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"unknown":1`) {
		t.Fatalf("expected unknown registered node: %s", rec.Body.String())
	}
}

func TestEdgeHealthExpectedEndpointAcceptsBearerTokenWithoutTrailingSlash(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := newHealthTestServer(t, st, telegram.Config{})

	req := httptest.NewRequest(http.MethodPut, "/edge-health/expected", strings.NewReader(`{"nodes":[{"vps_name":"vps-1"}]}`))
	req.Header.Set("Authorization", "Bearer health-secret")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEdgeHealthTelegramAlertDoesNotDuplicateSameState(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer api.Close()

	server := newHealthTestServer(t, st, telegram.Config{
		Enabled:  true,
		BotToken: "token",
		ChatIDs:  []string{"123"},
		APIBase:  api.URL,
	})

	req := httptest.NewRequest(http.MethodPost, "/edge-health/health-secret", strings.NewReader(edgeHealthReportJSON(edgehealth.StatusOK)))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("ok status = %d body=%s", rec.Code, rec.Body.String())
	}

	degraded := edgeHealthReportJSON(edgehealth.StatusDegraded)
	req = httptest.NewRequest(http.MethodPost, "/edge-health/health-secret", strings.NewReader(degraded))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("degraded status = %d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case text := <-sent:
		if !strings.Contains(text, "edge health DEGRADED") || !strings.Contains(text, "alert_id: edge-health:vps-1:degraded") {
			t.Fatalf("unexpected alert text: %s", text)
		}
	case <-time.After(time.Second):
		t.Fatal("health alert was not sent")
	}

	req = httptest.NewRequest(http.MethodPost, "/edge-health/health-secret", strings.NewReader(degraded))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("second degraded status = %d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case text := <-sent:
		t.Fatalf("duplicate health alert sent: %s", text)
	case <-time.After(100 * time.Millisecond):
	}
}

func newHealthTestServer(t *testing.T, st *store.Store, tgConfig telegram.Config) *Server {
	t.Helper()
	tg, err := telegram.New(tgConfig)
	if err != nil {
		t.Fatal(err)
	}
	return NewServerWithDashboardTokenAndEdgeHealth("secret", "edge-secret", EdgeHealthConfig{
		Token:           "health-secret",
		DefaultInterval: 30 * time.Second,
		MissedReports:   3,
		BootstrapGrace:  2 * time.Minute,
		MonitorInterval: time.Hour,
	}, st, tg, "/ws", "/push", "dash-secret")
}

func edgeHealthReportJSON(status string) string {
	checkStatus := edgehealth.CheckStatusOK
	if status == edgehealth.StatusDegraded {
		checkStatus = edgehealth.CheckStatusFailed
	}
	return `{"vps_name":"vps-1","vps_public_ip":"203.0.113.20","vps_internal_ip":"192.0.2.2","status":"` + status + `","interval_seconds":30,"missed_reports":3,"checked_at":"2026-07-08T12:00:00Z","checks":[{"name":"logger_health","target":"http://192.0.2.10:8080/healthz","status":"` + checkStatus + `","required":true}]}`
}
