package edgeapp

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr     string
	TargetAddr     string
	DataDir        string
	VPSName        string
	VPSPublicIP    string
	VPSPort        int
	ForwardEnabled bool
	ForwardURL     string
	ForwardToken   string
	ForwardTimeout time.Duration
	DialTimeout    time.Duration
	IdleTimeout    time.Duration
	MaxConnections int
	MaxAcceptRate  int
}

func LoadConfig() (Config, error) {
	hostname, _ := os.Hostname()
	config := Config{
		ListenAddr:     envOrDefault("EDGE_LISTEN_ADDR", ":3232"),
		TargetAddr:     strings.TrimSpace(os.Getenv("EDGE_TARGET_ADDR")),
		DataDir:        envOrDefault("EDGE_DATA_DIR", "/data"),
		VPSName:        envOrDefault("VPS_NAME", hostname),
		VPSPublicIP:    strings.TrimSpace(os.Getenv("VPS_PUBLIC_IP")),
		VPSPort:        parseInt(os.Getenv("VPS_PUBLIC_PORT")),
		ForwardEnabled: parseBool(os.Getenv("EDGE_FORWARD_ENABLED")),
		ForwardURL:     strings.TrimSpace(os.Getenv("EDGE_FORWARD_URL")),
		ForwardToken:   strings.TrimSpace(os.Getenv("EDGE_FORWARD_TOKEN")),
		ForwardTimeout: parseDurationOrDefault(os.Getenv("EDGE_FORWARD_TIMEOUT"), 5*time.Second),
		DialTimeout:    parseDurationOrDefault(os.Getenv("EDGE_DIAL_TIMEOUT"), 10*time.Second),
		IdleTimeout:    parseDurationOrDefault(os.Getenv("EDGE_IDLE_TIMEOUT"), 10*time.Minute),
		MaxConnections: parsePositiveIntOrDefault(os.Getenv("EDGE_MAX_CONNECTIONS"), 1024),
		MaxAcceptRate:  parsePositiveIntOrDefault(os.Getenv("EDGE_MAX_ACCEPTS_PER_SECOND"), 100),
	}
	if config.TargetAddr == "" {
		return Config{}, errors.New("EDGE_TARGET_ADDR is required")
	}
	if config.ForwardEnabled && config.ForwardURL == "" {
		return Config{}, errors.New("EDGE_FORWARD_URL is required when EDGE_FORWARD_ENABLED is true")
	}
	return config, nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on", "enabled":
		return true
	default:
		return false
	}
}

func parseInt(value string) int {
	num, _ := strconv.Atoi(strings.TrimSpace(value))
	return num
}

func parsePositiveIntOrDefault(value string, fallback int) int {
	num, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || num <= 0 {
		return fallback
	}
	return num
}

func parseDurationOrDefault(value string, fallback time.Duration) time.Duration {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}
