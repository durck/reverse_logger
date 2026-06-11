package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/durck/reverse_logger/internal/nginxedge"
)

func main() {
	config, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	forwarder := nginxedge.New(config)
	captureMode := strings.ToLower(envOrDefault("NGINX_EDGE_CAPTURE_MODE", "mirror"))
	if captureMode != "mirror" && captureMode != "tail" {
		log.Fatalf("NGINX_EDGE_CAPTURE_MODE must be mirror or tail, got %q", captureMode)
	}
	if captureMode == "mirror" && strings.TrimSpace(config.LogPath) != "" {
		log.Fatal("NGINX_EDGE_LOG_PATH must be empty in mirror mode to avoid duplicate WSS ingress events")
	}
	if captureMode == "tail" && strings.TrimSpace(config.LogPath) == "" {
		log.Fatal("NGINX_EDGE_LOG_PATH is required in tail mode")
	}

	if err := os.MkdirAll(config.SpoolDir, 0o750); err != nil {
		log.Fatal(err)
	}

	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), config.HTTPTimeout)
			if err := forwarder.Flush(ctx); err != nil {
				log.Printf("flush failed: %v", err)
			}
			cancel()
			time.Sleep(config.ForwardDelay)
		}
	}()

	if captureMode == "mirror" {
		go func() {
			log.Printf("nginx edge capture listening on %s", config.ListenAddr)
			if err := http.ListenAndServe(config.ListenAddr, forwarder.Handler()); err != nil {
				log.Fatal(err)
			}
		}()
	} else {
		if err := tail(config, forwarder); err != nil {
			log.Fatal(err)
		}
	}
	select {}
}

func loadConfig() (nginxedge.Config, error) {
	config := nginxedge.Config{
		LogPath:       strings.TrimSpace(os.Getenv("NGINX_EDGE_LOG_PATH")),
		SpoolDir:      envOrDefault("NGINX_EDGE_SPOOL_DIR", "/var/lib/reverse-logger/nginx-edge-spool"),
		ListenAddr:    envOrDefault("NGINX_EDGE_LISTEN_ADDR", "127.0.0.1:18080"),
		ForwardURL:    strings.TrimSpace(os.Getenv("NGINX_EDGE_FORWARD_URL")),
		ForwardToken:  strings.TrimSpace(os.Getenv("EDGE_FORWARD_TOKEN")),
		VPSName:       strings.TrimSpace(os.Getenv("VPS_NAME")),
		VPSPublicIP:   strings.TrimSpace(os.Getenv("VPS_PUBLIC_IP")),
		VPSInternalIP: strings.TrimSpace(os.Getenv("VPS_INTERNAL_IP")),
		WSPath:        envOrDefault("RSSH_WS_PATH", "/ws"),
		PushPath:      envOrDefault("RSSH_PUSH_PATH", "/push"),
		ForwardDelay:  parseDurationOrDefault(os.Getenv("NGINX_EDGE_FORWARD_INTERVAL"), time.Second),
		HTTPTimeout:   parseDurationOrDefault(os.Getenv("NGINX_EDGE_FORWARD_TIMEOUT"), 5*time.Second),
	}
	if config.ForwardURL == "" {
		return nginxedge.Config{}, errors.New("NGINX_EDGE_FORWARD_URL is required")
	}
	if config.ForwardToken == "" {
		return nginxedge.Config{}, errors.New("EDGE_FORWARD_TOKEN is required")
	}
	if config.VPSName == "" {
		hostname, _ := os.Hostname()
		config.VPSName = hostname
	}
	return config, nil
}

func tail(config nginxedge.Config, forwarder *nginxedge.Forwarder) error {
	offsetPath := config.SpoolDir + "/nginx-edge.offset"
	offset, err := readOffset(offsetPath)
	if err != nil {
		return err
	}

	for {
		file, err := os.Open(config.LogPath)
		if err != nil {
			log.Printf("open log failed: %v", err)
			time.Sleep(config.ForwardDelay)
			continue
		}
		nextOffset, err := normalizeTailOffset(file, offset)
		if err != nil {
			log.Printf("stat log failed: %v", err)
			_ = file.Close()
			time.Sleep(config.ForwardDelay)
			continue
		}
		if nextOffset != offset {
			offset = nextOffset
			if err := writeOffset(offsetPath, offset); err != nil {
				log.Printf("write offset failed: %v", err)
			}
		}
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			_ = file.Close()
			offset = 0
			continue
		}

		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			offset += int64(len(line)) + 1
			event, ok, err := forwarder.ParseLine(line)
			if err != nil {
				log.Printf("parse nginx edge line failed: %v", err)
				if err := writeOffset(offsetPath, offset); err != nil {
					log.Printf("write offset failed: %v", err)
				}
				continue
			}
			if ok {
				if err := forwarder.SpoolEvent(event); err != nil {
					_ = file.Close()
					return err
				}
			}
			if err := writeOffset(offsetPath, offset); err != nil {
				log.Printf("write offset failed: %v", err)
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("scan nginx edge log failed: %v", err)
		}
		_ = file.Close()
		time.Sleep(config.ForwardDelay)
	}
}

func normalizeTailOffset(file *os.File, offset int64) (int64, error) {
	if offset < 0 {
		return file.Seek(0, io.SeekEnd)
	}
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if offset > info.Size() {
		return 0, nil
	}
	return offset, nil
}

func readOffset(path string) (int64, error) {
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return -1, nil
	}
	if err != nil {
		return 0, err
	}
	offset, err := strconv.ParseInt(strings.TrimSpace(string(content)), 10, 64)
	if err != nil {
		return 0, err
	}
	return offset, nil
}

func writeOffset(path string, offset int64) error {
	return os.WriteFile(path, []byte(strconv.FormatInt(offset, 10)+"\n"), 0o640)
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
