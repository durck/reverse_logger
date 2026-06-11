package loggerapp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/durck/reverse_logger/internal/events"
	"github.com/durck/reverse_logger/internal/store"
	"github.com/durck/reverse_logger/internal/telegram"
)

type Server struct {
	webhookToken     string
	edgeForwardToken string
	ingressWSPath    string
	ingressPushPath  string
	store            *store.Store
	telegram         *telegram.Client
}

func NewServer(webhookToken, edgeForwardToken string, store *store.Store, telegramClient *telegram.Client) *Server {
	return NewServerWithIngressPaths(webhookToken, edgeForwardToken, store, telegramClient, events.DefaultWSPath, events.DefaultPushPath)
}

func NewServerWithIngressPaths(webhookToken, edgeForwardToken string, store *store.Store, telegramClient *telegram.Client, ingressWSPath, ingressPushPath string) *Server {
	return &Server{
		webhookToken:     webhookToken,
		edgeForwardToken: edgeForwardToken,
		ingressWSPath:    events.NormalizeIngressPath(ingressWSPath, events.DefaultWSPath),
		ingressPushPath:  events.NormalizeIngressPath(ingressPushPath, events.DefaultPushPath),
		store:            store,
		telegram:         telegramClient,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/hooks/", s.handleWebhook)
	mux.HandleFunc("/edge-events/", s.handleEdgeEvent)
	mux.HandleFunc("/ingress-events/", s.handleIngressEvent)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := s.store.Ping(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !tokenMatches(tokenFromPath(r.URL.Path, "/hooks/"), s.webhookToken) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	event, err := events.ParseWebhookPayload(body, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	inserted, err := s.store.InsertEvent(event)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if inserted && s.telegram.Enabled() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.telegram.NotifyEvent(ctx, event); err != nil {
				log.Printf("telegram alert failed: %v", err)
			}
		}()
	}

	enriched, enrichedInserted, err := s.store.EnrichAndStoreEvent(event)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":              "accepted",
		"event_hash":          event.EventHash,
		"duplicate":           !inserted,
		"enriched_event_hash": enriched.EventHash,
		"enriched_duplicate":  !enrichedInserted,
		"correlation_status":  enriched.CorrelationStatus,
	})
}

func (s *Server) handleEdgeEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !tokenMatches(tokenFromPath(r.URL.Path, "/edge-events/"), s.edgeForwardToken) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var event events.EdgeEvent
	if err := json.Unmarshal(body, &event); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	event.VPSName = strings.TrimSpace(event.VPSName)
	event.VPSPublicIP = strings.TrimSpace(event.VPSPublicIP)
	event.ClientIP = strings.TrimSpace(event.ClientIP)
	if event.VPSName == "" {
		writeError(w, http.StatusBadRequest, "vps_name is required")
		return
	}
	if event.ClientIP == "" || net.ParseIP(event.ClientIP) == nil {
		writeError(w, http.StatusBadRequest, "client_ip must be a valid IP address")
		return
	}
	if event.VPSPort < 0 || event.VPSPort > 65535 || event.ClientPort < 0 || event.ClientPort > 65535 {
		writeError(w, http.StatusBadRequest, "ports must be between 0 and 65535")
		return
	}
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = time.Now().UTC()
	} else {
		event.ReceivedAt = event.ReceivedAt.UTC()
	}
	event.RawJSON = append([]byte(nil), body...)
	event.EventHash = events.HashEdgeEvent(event)

	inserted, err := s.store.InsertEdgeEvent(event)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":     "accepted",
		"event_hash": event.EventHash,
		"duplicate":  !inserted,
	})
}

func (s *Server) handleIngressEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !tokenMatches(tokenFromPath(r.URL.Path, "/ingress-events/"), s.edgeForwardToken) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var event events.IngressEvent
	if err := json.Unmarshal(body, &event); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	event.RawJSON = append([]byte(nil), body...)
	event, err = events.NormalizeIngressEvent(event, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := events.ValidateIngressRoute(event, s.ingressWSPath, s.ingressPushPath); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	inserted, err := s.store.InsertIngressEvent(event)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	reconciled, err := s.store.ReconcileIngressEvent(event)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":     "accepted",
		"event_hash": event.EventHash,
		"duplicate":  !inserted,
		"reconciled": reconciled,
	})
}

func tokenFromPath(path, prefix string) string {
	token := strings.TrimPrefix(path, prefix)
	token = strings.Trim(token, "/")
	if strings.Contains(token, "/") {
		return ""
	}
	return token
}

func tokenMatches(got, want string) bool {
	got = strings.TrimSpace(got)
	want = strings.TrimSpace(want)
	if got == "" || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
