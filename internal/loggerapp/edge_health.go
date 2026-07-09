package loggerapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/durck/reverse_logger/internal/edgehealth"
	"github.com/durck/reverse_logger/internal/store"
	"github.com/durck/reverse_logger/internal/telegram"
)

func (s *Server) handleEdgeHealthReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !tokenMatches(edgeAuthToken(r, "/edge-health/"), s.edgeHealth.Token) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var report edgehealth.Report
	if err := json.Unmarshal(body, &report); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	report, err = edgehealth.NormalizeReport(report, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	transition, shouldAlert, err := s.store.RecordEdgeHealthReport(report, body, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if shouldAlert {
		go s.notifyTelegramEdgeHealthTransition(transition)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":          "accepted",
		"vps_name":        report.VPSName,
		"effective_state": transition.CurrentStatus,
		"alert_queued":    shouldAlert,
	})
}

func (s *Server) handleEdgeHealthExpected(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !tokenMatches(edgeAuthToken(r, "/edge-health/expected/"), s.edgeHealth.Token) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var payload edgehealth.ExpectedPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payload, err = edgehealth.NormalizeExpectedPayload(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	bootstrapGrace := s.edgeHealth.BootstrapGrace
	if payload.BootstrapGraceSeconds > 0 {
		bootstrapGrace = time.Duration(payload.BootstrapGraceSeconds) * time.Second
	}
	if err := s.store.RegisterEdgeHealthExpected(payload.Nodes, bootstrapGrace, time.Now().UTC()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "accepted",
		"nodes":  len(payload.Nodes),
	})
}

func (s *Server) handleDashboardEdgeHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireDashboardAuth(w, r) {
		return
	}
	if r.Method == http.MethodDelete {
		s.handleDashboardEdgeHealthDelete(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	overview, err := s.store.EdgeHealthOverview(
		ctx,
		time.Now().UTC(),
		s.edgeHealth.DefaultInterval,
		s.edgeHealth.MissedReports,
		s.edgeHealth.BootstrapGrace,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) handleDashboardEdgeHealthDelete(w http.ResponseWriter, r *http.Request) {
	vpsName := strings.TrimSpace(r.URL.Query().Get("vps_name"))
	if vpsName == "" {
		writeError(w, http.StatusBadRequest, "vps_name is required")
		return
	}
	deleted, err := s.store.DeleteEdgeHealthNode(vpsName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   map[bool]string{true: "deleted", false: "not_found"}[deleted],
		"deleted":  deleted,
		"vps_name": vpsName,
	})
}

func (s *Server) StartEdgeHealthMonitor(ctx context.Context) {
	ticker := time.NewTicker(s.edgeHealth.MonitorInterval)
	defer ticker.Stop()
	for {
		s.evaluateEdgeHealth(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) evaluateEdgeHealth(ctx context.Context) {
	evalCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	transitions, err := s.store.EvaluateEdgeHealthTransitions(
		evalCtx,
		time.Now().UTC(),
		s.edgeHealth.DefaultInterval,
		s.edgeHealth.MissedReports,
		s.edgeHealth.BootstrapGrace,
	)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Printf("edge health evaluation failed: %v", err)
		}
		return
	}
	for _, transition := range transitions {
		s.notifyTelegramEdgeHealthTransition(transition)
	}
}

func (s *Server) notifyTelegramEdgeHealthTransition(transition store.EdgeHealthTransition) {
	if !s.telegram.Enabled() {
		return
	}
	alert := telegram.HealthAlert{
		VPSName:          transition.Node.VPSName,
		PreviousStatus:   transition.PreviousStatus,
		Status:           transition.CurrentStatus,
		VPSPublicIP:      transition.Node.VPSPublicIP,
		VPSInternalIP:    transition.Node.VPSInternalIP,
		FailedChecks:     transition.Node.FailedChecks,
		LastReportStatus: transition.Node.LastReportStatus,
		LastSeenAt:       transition.Node.LastSeenAt,
		LastOKAt:         transition.Node.LastOKAt,
		CheckedAt:        transition.Node.CheckedAt,
		StaleAfter:       transition.Node.StaleAfter,
		IntervalSeconds:  transition.Node.IntervalSeconds,
		MissedReports:    transition.Node.MissedReports,
		AlertID:          healthAlertID(transition.Node.VPSName, transition.CurrentStatus),
	}
	message := telegram.FormatHealthAlert(alert)
	chatIDs := s.telegram.ChatIDs()
	errs := make([]error, len(chatIDs))
	var wg sync.WaitGroup
	for i, chatID := range chatIDs {
		i, chatID := i, chatID
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := s.telegram.SendFormattedMessageWithResult(ctx, chatID, message); err != nil {
				errs[i] = fmt.Errorf("recipient %d: %w", i+1, err)
			}
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		log.Printf("edge health telegram alert failed: %v", err)
		return
	}
	if err := s.store.MarkEdgeHealthNotified(transition.Node.VPSName, transition.CurrentStatus); err != nil {
		log.Printf("edge health notification marker failed: %v", err)
	}
}

func healthAlertID(vpsName, status string) string {
	vpsName = strings.TrimSpace(vpsName)
	status = strings.TrimSpace(status)
	if vpsName == "" {
		return "edge-health:" + status
	}
	return "edge-health:" + vpsName + ":" + status
}
