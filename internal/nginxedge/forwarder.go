package nginxedge

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/durck/reverse_logger/internal/events"
)

type Config struct {
	LogPath       string
	SpoolDir      string
	ListenAddr    string
	ForwardURL    string
	ForwardToken  string
	VPSName       string
	VPSPublicIP   string
	VPSInternalIP string
	WSPath        string
	PushPath      string
	ForwardDelay  time.Duration
	HTTPTimeout   time.Duration
}

type NginxLogEntry struct {
	Timestamp         string `json:"ts"`
	RequestID         string `json:"request_id"`
	RemoteAddr        string `json:"remote_addr"`
	RemotePort        string `json:"remote_port"`
	Host              string `json:"host"`
	RequestMethod     string `json:"request_method"`
	RequestURI        string `json:"request_uri"`
	URI               string `json:"uri"`
	Args              string `json:"args"`
	Status            int    `json:"status"`
	HTTPUpgrade       string `json:"http_upgrade"`
	HTTPConnection    string `json:"http_connection"`
	HTTPUserAgent     string `json:"http_user_agent"`
	HTTPXForwardedFor string `json:"http_x_forwarded_for"`
	ServerAddr        string `json:"server_addr"`
	Transport         string `json:"transport"`
}

type Forwarder struct {
	config Config
	client *http.Client
}

func New(config Config) *Forwarder {
	config.WSPath = normalizePath(defaultString(config.WSPath, "/ws"))
	config.PushPath = normalizePath(defaultString(config.PushPath, "/push"))
	if config.ForwardDelay <= 0 {
		config.ForwardDelay = time.Second
	}
	if config.HTTPTimeout <= 0 {
		config.HTTPTimeout = 5 * time.Second
	}
	return &Forwarder{
		config: config,
		client: &http.Client{Timeout: config.HTTPTimeout},
	}
}

func (f *Forwarder) ParseLine(line []byte) (events.IngressEvent, bool, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return events.IngressEvent{}, false, nil
	}

	var entry NginxLogEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return events.IngressEvent{}, false, err
	}

	uriPath := strings.TrimSpace(entry.URI)
	if uriPath == "" {
		uriPath = pathFromRequestURI(entry.RequestURI)
	}
	method := strings.ToUpper(strings.TrimSpace(entry.RequestMethod))
	pollingKeySHA1 := ""
	if key := queryValue(entry.RequestURI, entry.Args, "key"); key != "" {
		sum := sha1.Sum([]byte(key))
		pollingKeySHA1 = hex.EncodeToString(sum[:])
	}
	transport := classifyTransport(method, uriPath, entry.HTTPUpgrade, pollingKeySHA1, f.config.WSPath, f.config.PushPath)
	if entry.Transport != "" {
		explicitTransport := strings.ToLower(strings.TrimSpace(entry.Transport))
		if transport == "" || explicitTransport != transport {
			return events.IngressEvent{}, false, fmt.Errorf("transport %q does not match method/path", entry.Transport)
		}
	}
	if transport == "" {
		return events.IngressEvent{}, false, nil
	}

	receivedAt := time.Now().UTC()
	if strings.TrimSpace(entry.Timestamp) != "" {
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.Timestamp)); err == nil {
			receivedAt = parsed.UTC()
		}
	}

	clientPort, _ := strconv.Atoi(strings.TrimSpace(entry.RemotePort))
	rawHeaders, _ := json.Marshal(map[string]string{
		"upgrade":         entry.HTTPUpgrade,
		"connection":      entry.HTTPConnection,
		"user_agent":      entry.HTTPUserAgent,
		"x_forwarded_for": entry.HTTPXForwardedFor,
	})
	event := events.IngressEvent{
		RequestID:       entry.RequestID,
		Transport:       transport,
		VPSName:         f.config.VPSName,
		VPSPublicIP:     f.config.VPSPublicIP,
		VPSInternalIP:   strings.TrimSpace(f.config.VPSInternalIP),
		ClientIP:        entry.RemoteAddr,
		ClientPort:      clientPort,
		Host:            entry.Host,
		URI:             uriPath,
		Method:          method,
		UserAgent:       entry.HTTPUserAgent,
		Upgrade:         entry.HTTPUpgrade,
		Connection:      entry.HTTPConnection,
		XForwardedFor:   entry.HTTPXForwardedFor,
		PollingKeySHA1:  pollingKeySHA1,
		NginxReceivedAt: receivedAt,
		ForwardedAt:     time.Now().UTC(),
		RawHeaders:      rawHeaders,
		RawJSON:         append([]byte(nil), line...),
	}
	event, err := events.NormalizeIngressEvent(event, time.Now())
	if err != nil {
		return events.IngressEvent{}, false, err
	}
	return event, true, nil
}

func (f *Forwarder) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/capture", f.handleCapture)
	return mux
}

func (f *Forwarder) handleCapture(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	entry := NginxLogEntry{
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		RequestID:         r.Header.Get("X-Request-ID"),
		RemoteAddr:        firstNonEmpty(r.Header.Get("X-Real-IP"), r.Header.Get("X-Original-Remote-Addr")),
		RemotePort:        r.Header.Get("X-Original-Remote-Port"),
		Host:              firstNonEmpty(r.Header.Get("X-Original-Host"), r.Host),
		RequestMethod:     firstNonEmpty(r.Header.Get("X-Original-Method"), r.Method),
		RequestURI:        firstNonEmpty(r.Header.Get("X-Original-URI"), r.URL.RequestURI()),
		URI:               r.Header.Get("X-Original-Path"),
		Args:              r.Header.Get("X-Original-Args"),
		HTTPUpgrade:       r.Header.Get("X-Original-Upgrade"),
		HTTPConnection:    r.Header.Get("X-Original-Connection"),
		HTTPUserAgent:     r.Header.Get("X-Original-User-Agent"),
		HTTPXForwardedFor: r.Header.Get("X-Forwarded-For"),
		ServerAddr:        r.Header.Get("X-Original-Server-Addr"),
		Transport:         r.Header.Get("X-RSSH-Transport"),
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	event, ok, err := f.ParseLine(payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if ok {
		if err := f.SpoolEvent(event); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

func (f *Forwarder) SpoolEvent(event events.IngressEvent) error {
	if strings.TrimSpace(f.config.SpoolDir) == "" {
		return errors.New("spool dir is required")
	}
	if err := os.MkdirAll(f.config.SpoolDir, 0o750); err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	finalPath := filepath.Join(f.config.SpoolDir, event.EventHash+".json")
	tempPath := finalPath + ".tmp"
	if err := os.WriteFile(tempPath, payload, 0o640); err != nil {
		return err
	}
	return os.Rename(tempPath, finalPath)
}

func (f *Forwarder) Flush(ctx context.Context) error {
	if strings.TrimSpace(f.config.ForwardURL) == "" {
		return errors.New("forward url is required")
	}
	entries, err := os.ReadDir(f.config.SpoolDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var firstErr error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(f.config.SpoolDir, entry.Name())
		payload, err := os.ReadFile(path)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := f.post(ctx, payload); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := os.Remove(path); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (f *Forwarder) post(ctx context.Context, payload []byte) error {
	endpoint := strings.TrimRight(strings.TrimSpace(f.config.ForwardURL), "/")
	token := strings.TrimSpace(f.config.ForwardToken)
	if token != "" && !strings.HasSuffix(endpoint, "/"+token) {
		endpoint += "/" + token
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("central ingress endpoint returned status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func classifyTransport(method, uriPath, upgrade, pollingKeySHA1, wsPath, pushPath string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	uriPath = normalizePath(uriPath)
	upgrade = strings.ToLower(strings.TrimSpace(upgrade))
	if method == http.MethodGet && uriPath == normalizePath(wsPath) && upgrade == "websocket" {
		return "wss"
	}
	if method == http.MethodHead && uriPath == normalizePath(pushPath) && pollingKeySHA1 != "" {
		return "https"
	}
	return ""
}

func pathFromRequestURI(requestURI string) string {
	if strings.TrimSpace(requestURI) == "" {
		return ""
	}
	u, err := url.ParseRequestURI(requestURI)
	if err != nil {
		return requestURI
	}
	return u.Path
}

func queryValue(requestURI, args, name string) string {
	if strings.TrimSpace(requestURI) != "" {
		u, err := url.ParseRequestURI(requestURI)
		if err == nil {
			if value := u.Query().Get(name); value != "" {
				return value
			}
		}
	}
	values, err := url.ParseQuery(strings.TrimPrefix(args, "?"))
	if err != nil {
		return ""
	}
	return values.Get(name)
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if path == "/" {
		return path
	}
	return strings.TrimRight(path, "/")
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
