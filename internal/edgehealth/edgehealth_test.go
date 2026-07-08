package edgehealth

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAggregateStatusIgnoresSkippedAndOptionalFailures(t *testing.T) {
	checks := []Check{
		{Name: "tcp", Status: CheckStatusOK, Required: true},
		{Name: "vpn", Status: CheckStatusSkipped, Required: false},
		{Name: "optional", Status: CheckStatusFailed, Required: false},
	}
	if got := AggregateStatus(checks); got != StatusOK {
		t.Fatalf("status = %q", got)
	}
}

func TestAggregateStatusDegradesOnRequiredFailure(t *testing.T) {
	checks := []Check{
		{Name: "tcp", Status: CheckStatusOK, Required: true},
		{Name: "logger", Status: CheckStatusFailed, Required: true},
	}
	if got := AggregateStatus(checks); got != StatusDegraded {
		t.Fatalf("status = %q", got)
	}
}

func TestNormalizeReportRejectsStatusMismatch(t *testing.T) {
	report := Report{
		VPSName: "vps-1",
		Status:  StatusOK,
		Checks: []Check{
			{Name: "logger", Status: CheckStatusFailed, Required: true},
		},
	}
	if _, err := NormalizeReport(report, time.Now()); err == nil {
		t.Fatal("expected status mismatch error")
	}
}

func TestLoadConfigParsesEnv(t *testing.T) {
	t.Setenv("EDGE_HEALTH_FORWARD_URL", "http://192.0.2.10:8080/edge-health")
	t.Setenv("EDGE_HEALTH_TOKEN", "health-token")
	t.Setenv("VPS_NAME", "vps-1")
	t.Setenv("VPS_PUBLIC_IP", "203.0.113.20")
	t.Setenv("VPS_INTERNAL_IP", "192.0.2.2")
	t.Setenv("EDGE_HEALTH_REVERSE_SSH_ADDR", "192.0.2.10:3232")
	t.Setenv("EDGE_HEALTH_LOGGER_HEALTH_URL", "http://192.0.2.10:8080/healthz")
	t.Setenv("EDGE_HEALTH_VPN_IFACE", "vpn_softether")
	t.Setenv("EDGE_HEALTH_SYSTEMD_SERVICES", "nginx, nginx-edge-forwarder")
	t.Setenv("EDGE_HEALTH_INTERVAL", "45s")
	t.Setenv("EDGE_HEALTH_TIMEOUT", "7s")
	t.Setenv("EDGE_HEALTH_MISSED_REPORTS", "4")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Interval != 45*time.Second || config.Timeout != 7*time.Second || config.MissedReports != 4 {
		t.Fatalf("unexpected timing config: %+v", config)
	}
	if len(config.SystemdServices) != 2 || config.SystemdServices[1] != "nginx-edge-forwarder" {
		t.Fatalf("services = %#v", config.SystemdServices)
	}
}

func TestExpectedPayloadAcceptsStringBootstrapGrace(t *testing.T) {
	var payload ExpectedPayload
	if err := json.Unmarshal([]byte(`{"bootstrap_grace_seconds":"120","nodes":[{"vps_name":"vps-1"}]}`), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.BootstrapGraceSeconds != 120 {
		t.Fatalf("bootstrap grace = %d", payload.BootstrapGraceSeconds)
	}
}
