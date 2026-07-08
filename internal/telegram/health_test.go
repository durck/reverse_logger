package telegram

import (
	"strings"
	"testing"
)

func TestFormatHealthAlertMessage(t *testing.T) {
	message := FormatHealthAlertMessage(HealthAlert{
		VPSName:        "vps-1",
		PreviousStatus: "ok",
		Status:         "degraded",
		VPSPublicIP:    "203.0.113.20",
		VPSInternalIP:  "192.0.2.2",
		FailedChecks:   []string{"logger_health", "systemd:nginx"},
		LastSeenAt:     "2026-07-08T12:00:00Z",
		AlertID:        "edge-health:vps-1:degraded",
	})
	for _, want := range []string{
		"edge health DEGRADED",
		"vps: vps-1",
		"previous: ok",
		"failed_checks: logger_health,systemd:nginx",
		"alert_id: edge-health:vps-1:degraded",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("message missing %q:\n%s", want, message)
		}
	}
}
