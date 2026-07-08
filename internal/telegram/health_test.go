package telegram

import (
	"strings"
	"testing"
)

func TestFormatHealthAlertMessage(t *testing.T) {
	message := FormatHealthAlert(HealthAlert{
		VPSName:          "vps-1",
		PreviousStatus:   "ok",
		Status:           "degraded",
		VPSPublicIP:      "203.0.113.20",
		VPSInternalIP:    "192.0.2.2",
		FailedChecks:     []string{"logger_health", "systemd:nginx"},
		LastReportStatus: "ok",
		LastSeenAt:       "2026-07-08T12:00:00Z",
		IntervalSeconds:  30,
		MissedReports:    3,
		AlertID:          "edge-health:vps-1:degraded",
	})
	for _, want := range []string{
		"edge health DEGRADED",
		"vps: vps-1",
		"state: ok -> degraded",
		"ip: 203.0.113.20 / 192.0.2.2",
		"report: ok",
		"failed: logger_health,systemd:nginx",
		"missed: 3 x 30s",
		"last_seen: 2026-07-08 12:00:00Z",
		"alert_id: edge-health:vps-1:degraded",
	} {
		if !strings.Contains(message.Plain, want) {
			t.Fatalf("plain message missing %q:\n%s", want, message.Plain)
		}
	}
	for _, want := range []string{"<p><b>vps</b><br><code>vps-1</code></p>", "<li><b>state</b> <code>ok -&gt; degraded</code></li>", "<li><b>missed</b> <code>3 x 30s</code></li>"} {
		if !strings.Contains(message.RichHTML, want) {
			t.Fatalf("rich message missing %q:\n%s", want, message.RichHTML)
		}
	}
	if strings.Contains(message.RichHTML, "<blockquote>") || strings.Contains(message.RichHTML, "<table") {
		t.Fatalf("rich message should avoid quote/table containers:\n%s", message.RichHTML)
	}
}

func TestFormatHealthAlertOmitsRecoveryNoise(t *testing.T) {
	message := FormatHealthAlert(HealthAlert{
		VPSName:          "vps-1",
		PreviousStatus:   "down",
		Status:           "ok",
		VPSPublicIP:      "203.0.113.20",
		VPSInternalIP:    "203.0.113.20",
		LastReportStatus: "ok",
		LastSeenAt:       "2026-07-08T12:01:00Z",
		LastOKAt:         "2026-07-08T12:01:00Z",
		IntervalSeconds:  30,
		MissedReports:    3,
		StaleAfter:       "2026-07-08T12:02:30Z",
		AlertID:          "edge-health:vps-1:ok",
	})
	for _, want := range []string{
		"edge health OK",
		"state: down -> ok",
		"ip: 203.0.113.20",
		"last_ok: 2026-07-08 12:01:00Z",
		"alert_id: edge-health:vps-1:ok",
	} {
		if !strings.Contains(message.Plain, want) {
			t.Fatalf("plain message missing %q:\n%s", want, message.Plain)
		}
	}
	for _, notWant := range []string{"report:", "failed:", "missed:", "stale_after", "203.0.113.20 / 203.0.113.20"} {
		if strings.Contains(message.Plain, notWant) {
			t.Fatalf("plain message should not contain %q:\n%s", notWant, message.Plain)
		}
	}
}
