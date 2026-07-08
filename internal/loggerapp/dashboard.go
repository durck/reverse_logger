package loggerapp

import (
	"context"
	"embed"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/durck/reverse_logger/internal/store"
)

//go:embed dashboard_static/index.html
var dashboardAssets embed.FS

func (s *Server) handleDashboardRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireDashboardAuth(w, r) {
		return
	}
	http.Redirect(w, r, "/dashboard/", http.StatusFound)
}

func (s *Server) handleDashboardPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireDashboardAuth(w, r) {
		return
	}
	if r.URL.Path != "/dashboard/" && r.URL.Path != "/dashboard/index.html" && r.URL.Path != "/dashboard/health" && r.URL.Path != "/dashboard/health/" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	body, err := dashboardAssets.ReadFile("dashboard_static/index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) handleDashboardOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireDashboardAuth(w, r) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	overview, err := s.store.DashboardOverview(ctx, parseDashboardWindow(r.URL.Query().Get("window")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) handleDashboardEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireDashboardAuth(w, r) {
		return
	}

	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	events, err := s.store.DashboardEvents(ctx, store.DashboardEventQuery{
		Window:            parseDashboardWindow(query.Get("window")),
		Status:            query.Get("status"),
		CorrelationStatus: query.Get("correlation_status"),
		Transport:         query.Get("transport"),
		Search:            query.Get("q"),
		Limit:             limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"events":    events,
		"max_limit": store.MaxDashboardEventLimit,
	})
}

func (s *Server) requireDashboardAuth(w http.ResponseWriter, r *http.Request) bool {
	if strings.TrimSpace(s.dashboardToken) == "" {
		writeError(w, http.StatusNotFound, "not found")
		return false
	}
	if tokenMatches(bearerToken(r.Header.Get("Authorization")), s.dashboardToken) {
		return true
	}
	if _, password, ok := r.BasicAuth(); ok && tokenMatches(password, s.dashboardToken) {
		return true
	}

	w.Header().Set("WWW-Authenticate", `Basic realm="rssh dashboard", charset="UTF-8"`)
	writeError(w, http.StatusUnauthorized, "authentication required")
	return false
}

func parseDashboardWindow(value string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}
