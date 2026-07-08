package edgehealth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	StatusOK       = "ok"
	StatusDegraded = "degraded"
	StatusDown     = "down"
	StatusUnknown  = "unknown"

	CheckStatusOK      = "ok"
	CheckStatusFailed  = "failed"
	CheckStatusSkipped = "skipped"

	DefaultInterval      = 30 * time.Second
	DefaultMissedReports = 3
	DefaultTimeout       = 5 * time.Second
)

type Config struct {
	ForwardURL         string
	Token              string
	VPSName            string
	VPSPublicIP        string
	VPSInternalIP      string
	MainReverseSSHAddr string
	LoggerHealthURL    string
	VPNIface           string
	SystemdServices    []string
	Interval           time.Duration
	Timeout            time.Duration
	MissedReports      int
}

type Check struct {
	Name      string `json:"name"`
	Target    string `json:"target,omitempty"`
	Status    string `json:"status"`
	Required  bool   `json:"required"`
	Message   string `json:"message,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}

type Report struct {
	VPSName         string    `json:"vps_name"`
	VPSPublicIP     string    `json:"vps_public_ip,omitempty"`
	VPSInternalIP   string    `json:"vps_internal_ip,omitempty"`
	Status          string    `json:"status"`
	Checks          []Check   `json:"checks"`
	IntervalSeconds int       `json:"interval_seconds"`
	MissedReports   int       `json:"missed_reports"`
	CheckedAt       time.Time `json:"checked_at"`
}

type ExpectedNode struct {
	VPSName       string `json:"vps_name"`
	VPSPublicIP   string `json:"vps_public_ip,omitempty"`
	VPSInternalIP string `json:"vps_internal_ip,omitempty"`
}

type ExpectedPayload struct {
	Nodes                 []ExpectedNode `json:"nodes"`
	BootstrapGraceSeconds FlexibleInt    `json:"bootstrap_grace_seconds,omitempty"`
}

type FlexibleInt int

func (value *FlexibleInt) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	raw = strings.Trim(raw, `"`)
	if raw == "" || raw == "null" {
		*value = 0
		return nil
	}
	num, err := strconv.Atoi(raw)
	if err != nil {
		return err
	}
	*value = FlexibleInt(num)
	return nil
}

func LoadConfig() (Config, error) {
	hostname, _ := os.Hostname()
	config := Config{
		ForwardURL:         strings.TrimSpace(os.Getenv("EDGE_HEALTH_FORWARD_URL")),
		Token:              strings.TrimSpace(os.Getenv("EDGE_HEALTH_TOKEN")),
		VPSName:            envOrDefault("VPS_NAME", hostname),
		VPSPublicIP:        strings.TrimSpace(os.Getenv("VPS_PUBLIC_IP")),
		VPSInternalIP:      strings.TrimSpace(os.Getenv("VPS_INTERNAL_IP")),
		MainReverseSSHAddr: strings.TrimSpace(os.Getenv("EDGE_HEALTH_REVERSE_SSH_ADDR")),
		LoggerHealthURL:    strings.TrimSpace(os.Getenv("EDGE_HEALTH_LOGGER_HEALTH_URL")),
		VPNIface:           strings.TrimSpace(os.Getenv("EDGE_HEALTH_VPN_IFACE")),
		SystemdServices:    splitList(os.Getenv("EDGE_HEALTH_SYSTEMD_SERVICES")),
		Interval:           parseDurationOrDefault(os.Getenv("EDGE_HEALTH_INTERVAL"), DefaultInterval),
		Timeout:            parseDurationOrDefault(os.Getenv("EDGE_HEALTH_TIMEOUT"), DefaultTimeout),
		MissedReports:      parsePositiveIntOrDefault(os.Getenv("EDGE_HEALTH_MISSED_REPORTS"), DefaultMissedReports),
	}
	if config.ForwardURL == "" {
		return Config{}, errors.New("EDGE_HEALTH_FORWARD_URL is required")
	}
	if config.Token == "" {
		return Config{}, errors.New("EDGE_HEALTH_TOKEN is required")
	}
	if config.VPSName == "" {
		return Config{}, errors.New("VPS_NAME is required")
	}
	if config.MainReverseSSHAddr == "" {
		return Config{}, errors.New("EDGE_HEALTH_REVERSE_SSH_ADDR is required")
	}
	if config.LoggerHealthURL == "" {
		return Config{}, errors.New("EDGE_HEALTH_LOGGER_HEALTH_URL is required")
	}
	return config, nil
}

func RunChecks(ctx context.Context, config Config) Report {
	checks := []Check{
		checkTCP(ctx, "reverse_ssh_tcp", config.MainReverseSSHAddr, config.Timeout),
		checkHTTP(ctx, "logger_health", config.LoggerHealthURL, config.Timeout),
	}
	if config.VPNIface == "" {
		checks = append(checks, Check{
			Name:     "vpn_iface",
			Status:   CheckStatusSkipped,
			Required: false,
			Message:  "EDGE_HEALTH_VPN_IFACE is empty",
		})
	} else {
		checks = append(checks, checkInterface(config.VPNIface))
	}
	for _, service := range config.SystemdServices {
		checks = append(checks, checkSystemd(ctx, service, config.Timeout))
	}

	return Report{
		VPSName:         strings.TrimSpace(config.VPSName),
		VPSPublicIP:     strings.TrimSpace(config.VPSPublicIP),
		VPSInternalIP:   strings.TrimSpace(config.VPSInternalIP),
		Status:          AggregateStatus(checks),
		Checks:          checks,
		IntervalSeconds: int(config.Interval.Seconds()),
		MissedReports:   config.MissedReports,
		CheckedAt:       time.Now().UTC(),
	}
}

func SendReport(ctx context.Context, config Config, report Report) error {
	report, err := NormalizeReport(report, time.Now().UTC())
	if err != nil {
		return err
	}
	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	endpoint := withPathToken(config.ForwardURL, config.Token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: config.Timeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("central health endpoint returned status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func NormalizeReport(report Report, now time.Time) (Report, error) {
	report.VPSName = strings.TrimSpace(report.VPSName)
	report.VPSPublicIP = strings.TrimSpace(report.VPSPublicIP)
	report.VPSInternalIP = strings.TrimSpace(report.VPSInternalIP)
	if report.VPSName == "" {
		return Report{}, errors.New("vps_name is required")
	}
	if report.CheckedAt.IsZero() {
		report.CheckedAt = now.UTC()
	} else {
		report.CheckedAt = report.CheckedAt.UTC()
	}
	if report.IntervalSeconds <= 0 {
		report.IntervalSeconds = int(DefaultInterval.Seconds())
	}
	if report.MissedReports <= 0 {
		report.MissedReports = DefaultMissedReports
	}
	for i := range report.Checks {
		report.Checks[i].Name = strings.TrimSpace(report.Checks[i].Name)
		report.Checks[i].Target = strings.TrimSpace(report.Checks[i].Target)
		report.Checks[i].Status = strings.ToLower(strings.TrimSpace(report.Checks[i].Status))
		report.Checks[i].Message = strings.TrimSpace(report.Checks[i].Message)
		if report.Checks[i].Name == "" {
			return Report{}, errors.New("check name is required")
		}
		switch report.Checks[i].Status {
		case CheckStatusOK, CheckStatusFailed, CheckStatusSkipped:
		default:
			return Report{}, fmt.Errorf("check %q has invalid status %q", report.Checks[i].Name, report.Checks[i].Status)
		}
	}
	computed := AggregateStatus(report.Checks)
	report.Status = strings.ToLower(strings.TrimSpace(report.Status))
	if report.Status == "" {
		report.Status = computed
	}
	if report.Status != StatusOK && report.Status != StatusDegraded {
		return Report{}, errors.New("status must be ok or degraded")
	}
	if report.Status != computed {
		return Report{}, fmt.Errorf("status %q does not match checks aggregate %q", report.Status, computed)
	}
	return report, nil
}

func NormalizeExpectedPayload(payload ExpectedPayload) (ExpectedPayload, error) {
	if len(payload.Nodes) == 0 {
		return ExpectedPayload{}, errors.New("nodes are required")
	}
	for i := range payload.Nodes {
		payload.Nodes[i].VPSName = strings.TrimSpace(payload.Nodes[i].VPSName)
		payload.Nodes[i].VPSPublicIP = strings.TrimSpace(payload.Nodes[i].VPSPublicIP)
		payload.Nodes[i].VPSInternalIP = strings.TrimSpace(payload.Nodes[i].VPSInternalIP)
		if payload.Nodes[i].VPSName == "" {
			return ExpectedPayload{}, errors.New("node vps_name is required")
		}
	}
	return payload, nil
}

func AggregateStatus(checks []Check) string {
	for _, check := range checks {
		if check.Required && check.Status == CheckStatusFailed {
			return StatusDegraded
		}
	}
	return StatusOK
}

func FailedCheckNames(checks []Check) []string {
	failed := make([]string, 0)
	for _, check := range checks {
		if check.Required && check.Status == CheckStatusFailed {
			failed = append(failed, check.Name)
		}
	}
	return failed
}

func checkTCP(ctx context.Context, name, addr string, timeout time.Duration) Check {
	start := time.Now()
	check := Check{Name: name, Target: addr, Required: true}
	if strings.TrimSpace(addr) == "" {
		check.Status = CheckStatusFailed
		check.Message = "target address is empty"
		return check
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	check.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		check.Status = CheckStatusFailed
		check.Message = err.Error()
		return check
	}
	_ = conn.Close()
	check.Status = CheckStatusOK
	return check
}

func checkHTTP(ctx context.Context, name, endpoint string, timeout time.Duration) Check {
	start := time.Now()
	check := Check{Name: name, Target: endpoint, Required: true}
	if strings.TrimSpace(endpoint) == "" {
		check.Status = CheckStatusFailed
		check.Message = "target URL is empty"
		return check
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		check.Status = CheckStatusFailed
		check.Message = err.Error()
		return check
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	check.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		check.Status = CheckStatusFailed
		check.Message = err.Error()
		return check
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		check.Status = CheckStatusFailed
		check.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return check
	}
	check.Status = CheckStatusOK
	return check
}

func checkInterface(name string) Check {
	check := Check{Name: "vpn_iface", Target: name, Required: true}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		check.Status = CheckStatusFailed
		check.Message = err.Error()
		return check
	}
	if iface.Flags&net.FlagUp == 0 {
		check.Status = CheckStatusFailed
		check.Message = "interface is down"
		return check
	}
	check.Status = CheckStatusOK
	return check
}

func checkSystemd(ctx context.Context, service string, timeout time.Duration) Check {
	service = strings.TrimSpace(service)
	check := Check{Name: "systemd:" + service, Target: service, Required: true}
	if service == "" {
		check.Status = CheckStatusSkipped
		check.Required = false
		check.Message = "service name is empty"
		return check
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "is-active", service).CombinedOutput()
	if err != nil {
		check.Status = CheckStatusFailed
		check.Message = strings.TrimSpace(string(out))
		if check.Message == "" {
			check.Message = err.Error()
		}
		return check
	}
	if strings.TrimSpace(string(out)) != "active" {
		check.Status = CheckStatusFailed
		check.Message = strings.TrimSpace(string(out))
		return check
	}
	check.Status = CheckStatusOK
	return check
}

func withPathToken(endpoint, token string) string {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	token = strings.TrimSpace(token)
	if token == "" || strings.HasSuffix(endpoint, "/"+token) {
		return endpoint
	}
	return endpoint + "/" + token
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func parseDurationOrDefault(value string, fallback time.Duration) time.Duration {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}

func parsePositiveIntOrDefault(value string, fallback int) int {
	num, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || num <= 0 {
		return fallback
	}
	return num
}
