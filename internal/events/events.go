package events

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultWSPath   = "/ws"
	DefaultPushPath = "/push"
)

type Event struct {
	EventHash            string          `json:"event_hash"`
	Status               string          `json:"status"`
	ReverseSSHID         string          `json:"reverse_ssh_id"`
	HostName             string          `json:"host_name"`
	UserName             string          `json:"user_name,omitempty"`
	ComputerName         string          `json:"computer_name,omitempty"`
	IPRaw                string          `json:"ip_raw,omitempty"`
	IPAddr               string          `json:"ip_addr,omitempty"`
	IPPort               int             `json:"ip_port,omitempty"`
	Transport            string          `json:"transport,omitempty"`
	PublicKeyFingerprint string          `json:"public_key_fingerprint,omitempty"`
	ProxySourceIP        string          `json:"proxy_source_ip,omitempty"`
	Version              string          `json:"version,omitempty"`
	SourceTS             time.Time       `json:"source_ts,omitempty"`
	ReceivedAt           time.Time       `json:"received_at"`
	RawJSON              json.RawMessage `json:"raw_json,omitempty"`
}

type EdgeEvent struct {
	EventHash   string          `json:"event_hash"`
	VPSName     string          `json:"vps_name"`
	VPSPublicIP string          `json:"vps_public_ip,omitempty"`
	VPSPort     int             `json:"vps_port,omitempty"`
	ClientIP    string          `json:"client_ip"`
	ClientPort  int             `json:"client_port,omitempty"`
	ReceivedAt  time.Time       `json:"received_at"`
	RawJSON     json.RawMessage `json:"raw_json,omitempty"`
}

type IngressEvent struct {
	EventHash       string          `json:"event_hash"`
	RequestID       string          `json:"request_id,omitempty"`
	Transport       string          `json:"transport"`
	VPSName         string          `json:"vps_name"`
	VPSPublicIP     string          `json:"vps_public_ip,omitempty"`
	VPSInternalIP   string          `json:"vps_internal_ip,omitempty"`
	ForwarderIP     string          `json:"forwarder_ip,omitempty"`
	ClientIP        string          `json:"client_ip"`
	ClientPort      int             `json:"client_port,omitempty"`
	Host            string          `json:"host,omitempty"`
	URI             string          `json:"uri,omitempty"`
	Method          string          `json:"method,omitempty"`
	UserAgent       string          `json:"user_agent,omitempty"`
	Upgrade         string          `json:"upgrade,omitempty"`
	Connection      string          `json:"connection,omitempty"`
	XForwardedFor   string          `json:"x_forwarded_for,omitempty"`
	PollingKeySHA1  string          `json:"polling_key_sha1,omitempty"`
	NginxReceivedAt time.Time       `json:"nginx_received_at"`
	ForwardedAt     time.Time       `json:"forwarded_at,omitempty"`
	RawHeaders      json.RawMessage `json:"raw_headers,omitempty"`
	RawJSON         json.RawMessage `json:"raw_json,omitempty"`
}

type ReverseSSHErrorEvent struct {
	EventHash   string          `json:"event_hash"`
	Source      string          `json:"source"`
	Unit        string          `json:"unit,omitempty"`
	Severity    string          `json:"severity"`
	Reason      string          `json:"reason"`
	Message     string          `json:"message"`
	RemoteAddr  string          `json:"remote_addr,omitempty"`
	RemoteIP    string          `json:"remote_ip,omitempty"`
	RemotePort  int             `json:"remote_port,omitempty"`
	Transport   string          `json:"transport,omitempty"`
	Host        string          `json:"host,omitempty"`
	Fingerprint string          `json:"fingerprint,omitempty"`
	ObservedAt  time.Time       `json:"observed_at"`
	ReceivedAt  time.Time       `json:"received_at"`
	RawJSON     json.RawMessage `json:"raw_json,omitempty"`
	RawLine     string          `json:"raw_line,omitempty"`
}

type EnrichedEvent struct {
	EventHash            string          `json:"event_hash"`
	SourceEventHash      string          `json:"source_event_hash"`
	IngressEventHash     string          `json:"ingress_event_hash,omitempty"`
	CorrelationStatus    string          `json:"correlation_status"`
	CorrelationMethod    string          `json:"correlation_method,omitempty"`
	Status               string          `json:"status"`
	ReverseSSHID         string          `json:"reverse_ssh_id"`
	HostName             string          `json:"host_name"`
	UserName             string          `json:"user_name,omitempty"`
	ComputerName         string          `json:"computer_name,omitempty"`
	IPRaw                string          `json:"ip_raw,omitempty"`
	IPAddr               string          `json:"ip_addr,omitempty"`
	IPPort               int             `json:"ip_port,omitempty"`
	RealClientIP         string          `json:"real_client_ip,omitempty"`
	ClientPort           int             `json:"client_port,omitempty"`
	Transport            string          `json:"transport,omitempty"`
	PublicKeyFingerprint string          `json:"public_key_fingerprint,omitempty"`
	ProxySourceIP        string          `json:"proxy_source_ip,omitempty"`
	VPSName              string          `json:"vps_name,omitempty"`
	VPSPublicIP          string          `json:"vps_public_ip,omitempty"`
	VPSInternalIP        string          `json:"vps_internal_ip,omitempty"`
	ForwarderIP          string          `json:"forwarder_ip,omitempty"`
	Version              string          `json:"version,omitempty"`
	SourceTS             time.Time       `json:"source_ts,omitempty"`
	ReceivedAt           time.Time       `json:"received_at"`
	IngressReceivedAt    time.Time       `json:"ingress_received_at,omitempty"`
	RawWebhookJSON       json.RawMessage `json:"raw_webhook_json,omitempty"`
	RawIngressJSON       json.RawMessage `json:"raw_ingress_json,omitempty"`
}

type clientState struct {
	Status    string    `json:"Status"`
	ID        string    `json:"ID"`
	IP        string    `json:"IP"`
	HostName  string    `json:"HostName"`
	Version   string    `json:"Version"`
	Timestamp time.Time `json:"Timestamp"`

	Transport            string `json:"Transport"`
	PublicKeyFingerprint string `json:"PublicKeyFingerprint"`
	ProxySourceIP        string `json:"ProxySourceIP"`
}

type webhookWrapper struct {
	Full string `json:"Full"`
	Text string `json:"text"`
}

func ParseWebhookPayload(body []byte, receivedAt time.Time) (Event, error) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return Event{}, errors.New("empty webhook body")
	}

	raw := json.RawMessage(append([]byte(nil), body...))

	var wrapper webhookWrapper
	if err := json.Unmarshal(body, &wrapper); err == nil && strings.TrimSpace(wrapper.Full) != "" {
		var state clientState
		if err := json.Unmarshal([]byte(wrapper.Full), &state); err != nil {
			return Event{}, err
		}
		event := eventFromClientState(state, receivedAt, raw)
		if err := validateEvent(event); err != nil {
			return Event{}, err
		}
		return event, nil
	}

	var state clientState
	if err := json.Unmarshal(body, &state); err != nil {
		return Event{}, err
	}
	event := eventFromClientState(state, receivedAt, raw)
	if err := validateEvent(event); err != nil {
		return Event{}, err
	}
	return event, nil
}

func eventFromClientState(state clientState, receivedAt time.Time, raw json.RawMessage) Event {
	userName, computerName := ParseHostName(state.HostName)
	ipAddr, ipPort := ParseEndpoint(state.IP)
	proxySourceIP, _ := ParseEndpoint(state.ProxySourceIP)
	event := Event{
		Status:               strings.ToLower(strings.TrimSpace(state.Status)),
		ReverseSSHID:         strings.TrimSpace(state.ID),
		HostName:             strings.TrimSpace(state.HostName),
		UserName:             userName,
		ComputerName:         computerName,
		IPRaw:                strings.TrimSpace(state.IP),
		IPAddr:               ipAddr,
		IPPort:               ipPort,
		Transport:            strings.ToLower(strings.TrimSpace(state.Transport)),
		PublicKeyFingerprint: strings.TrimSpace(state.PublicKeyFingerprint),
		ProxySourceIP:        proxySourceIP,
		Version:              strings.TrimSpace(state.Version),
		SourceTS:             state.Timestamp,
		ReceivedAt:           receivedAt.UTC(),
		RawJSON:              raw,
	}
	event.EventHash = HashEvent(event)
	return event
}

func validateEvent(event Event) error {
	if event.Status == "" {
		return errors.New("status is required")
	}
	if event.ReverseSSHID == "" {
		return errors.New("id is required")
	}
	if event.HostName == "" {
		return errors.New("host name is required")
	}
	return nil
}

func ParseHostName(hostName string) (string, string) {
	hostName = strings.TrimSpace(hostName)
	if hostName == "" {
		return "", ""
	}
	parts := strings.SplitN(hostName, ".", 2)
	if len(parts) == 1 {
		return "", hostName
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func ParseEndpoint(endpoint string) (string, int) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", 0
	}

	host, port, err := net.SplitHostPort(endpoint)
	if err == nil {
		portNum, _ := strconv.Atoi(port)
		return strings.Trim(host, "[]"), portNum
	}

	if strings.HasPrefix(endpoint, "[") && strings.Contains(endpoint, "]") {
		end := strings.Index(endpoint, "]")
		host := endpoint[1:end]
		rest := strings.TrimPrefix(endpoint[end+1:], ":")
		portNum, _ := strconv.Atoi(rest)
		return host, portNum
	}

	if ip := net.ParseIP(endpoint); ip != nil {
		return endpoint, 0
	}

	return endpoint, 0
}

func HashEvent(event Event) string {
	sourceTS := ""
	if !event.SourceTS.IsZero() {
		sourceTS = event.SourceTS.UTC().Format(time.RFC3339Nano)
	}
	parts := []string{
		strings.ToLower(event.Status),
		event.ReverseSSHID,
		sourceTS,
		event.IPRaw,
		event.HostName,
	}
	return hashStrings(parts...)
}

func NewEdgeEvent(vpsName, vpsPublicIP string, vpsPort int, clientIP string, clientPort int, receivedAt time.Time, raw json.RawMessage) EdgeEvent {
	event := EdgeEvent{
		VPSName:     strings.TrimSpace(vpsName),
		VPSPublicIP: strings.TrimSpace(vpsPublicIP),
		VPSPort:     vpsPort,
		ClientIP:    strings.TrimSpace(clientIP),
		ClientPort:  clientPort,
		ReceivedAt:  receivedAt.UTC(),
		RawJSON:     raw,
	}
	event.EventHash = HashEdgeEvent(event)
	return event
}

func HashEdgeEvent(event EdgeEvent) string {
	parts := []string{
		event.VPSName,
		event.VPSPublicIP,
		strconv.Itoa(event.VPSPort),
		event.ClientIP,
		strconv.Itoa(event.ClientPort),
		event.ReceivedAt.UTC().Format(time.RFC3339Nano),
	}
	return hashStrings(parts...)
}

func NormalizeIngressEvent(event IngressEvent, receivedAt time.Time) (IngressEvent, error) {
	event.Transport = strings.ToLower(strings.TrimSpace(event.Transport))
	event.RequestID = strings.TrimSpace(event.RequestID)
	event.VPSName = strings.TrimSpace(event.VPSName)
	event.VPSPublicIP = strings.TrimSpace(event.VPSPublicIP)
	event.VPSInternalIP = strings.TrimSpace(event.VPSInternalIP)
	event.ForwarderIP = strings.TrimSpace(event.ForwarderIP)
	event.ClientIP = strings.TrimSpace(event.ClientIP)
	event.Host = strings.TrimSpace(event.Host)
	event.URI = strings.TrimSpace(event.URI)
	event.Method = strings.ToUpper(strings.TrimSpace(event.Method))
	event.UserAgent = strings.TrimSpace(event.UserAgent)
	event.Upgrade = strings.TrimSpace(event.Upgrade)
	event.Connection = strings.TrimSpace(event.Connection)
	event.XForwardedFor = strings.TrimSpace(event.XForwardedFor)
	event.PollingKeySHA1 = strings.ToLower(strings.TrimSpace(event.PollingKeySHA1))
	if event.NginxReceivedAt.IsZero() {
		event.NginxReceivedAt = receivedAt.UTC()
	} else {
		event.NginxReceivedAt = event.NginxReceivedAt.UTC()
	}
	if event.ForwardedAt.IsZero() {
		event.ForwardedAt = receivedAt.UTC()
	} else {
		event.ForwardedAt = event.ForwardedAt.UTC()
	}
	if err := validateIngressEvent(event); err != nil {
		return IngressEvent{}, err
	}
	event.EventHash = HashIngressEvent(event)
	return event, nil
}

func validateIngressEvent(event IngressEvent) error {
	if event.Transport != "wss" && event.Transport != "https" {
		return errors.New("transport must be wss or https")
	}
	if event.VPSName == "" {
		return errors.New("vps_name is required")
	}
	if event.ClientIP == "" || net.ParseIP(event.ClientIP) == nil {
		return errors.New("client_ip must be a valid IP address")
	}
	if event.ForwarderIP != "" && net.ParseIP(event.ForwarderIP) == nil {
		return errors.New("forwarder_ip must be a valid IP address")
	}
	if event.ClientPort < 0 || event.ClientPort > 65535 {
		return errors.New("client_port must be between 0 and 65535")
	}
	if event.URI == "" {
		return errors.New("uri is required")
	}
	if event.Method == "" {
		return errors.New("method is required")
	}
	switch event.Transport {
	case "wss":
		if event.Method != "GET" {
			return errors.New("wss ingress must use GET")
		}
		if strings.ToLower(event.Upgrade) != "websocket" {
			return errors.New("wss ingress must include websocket upgrade")
		}
	case "https":
		if event.Method != "HEAD" {
			return errors.New("https ingress must use HEAD")
		}
		if event.PollingKeySHA1 == "" {
			return errors.New("https ingress requires polling_key_sha1")
		}
		if !isSHA1Hex(event.PollingKeySHA1) {
			return errors.New("https ingress polling_key_sha1 must be 40 hex characters")
		}
	}
	return nil
}

func ValidateIngressRoute(event IngressEvent, wsPath, pushPath string) error {
	path, err := ingressURIPath(event.URI)
	if err != nil {
		return err
	}
	switch event.Transport {
	case "wss":
		if !ingressPathAllowed(path, wsPath, DefaultWSPath) {
			return errors.New("wss ingress uri does not match configured ws path")
		}
	case "https":
		if !ingressPathAllowed(path, pushPath, DefaultPushPath) {
			return errors.New("https ingress uri does not match configured push path")
		}
	}
	return nil
}

func ParseIngressPaths(configured, fallback string) []string {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return []string{NormalizeIngressPath(fallback, fallback)}
	}
	parts := strings.Split(configured, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = NormalizeIngressPath(part, "")
		if part == "" || part == "/" {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return []string{NormalizeIngressPath(fallback, fallback)}
	}
	return out
}

func ingressPathAllowed(path, configured, fallback string) bool {
	for _, candidate := range ParseIngressPaths(configured, fallback) {
		if path == candidate {
			return true
		}
	}
	return false
}

func NormalizeIngressPath(path, fallback string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = fallback
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return path
}

func ingressURIPath(rawURI string) (string, error) {
	parsed, err := url.ParseRequestURI(rawURI)
	if err == nil {
		return NormalizeIngressPath(parsed.Path, "/"), nil
	}
	path, _, _ := strings.Cut(rawURI, "?")
	path = NormalizeIngressPath(path, "/")
	if path == "" || path == "/" {
		return "", errors.New("uri path is required")
	}
	return path, nil
}

func isSHA1Hex(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}

func HashIngressEvent(event IngressEvent) string {
	parts := []string{
		event.RequestID,
		event.Transport,
		event.VPSName,
		event.VPSPublicIP,
		event.VPSInternalIP,
		event.ClientIP,
		strconv.Itoa(event.ClientPort),
		event.Host,
		event.URI,
		event.Method,
		event.PollingKeySHA1,
		event.NginxReceivedAt.UTC().Format(time.RFC3339Nano),
	}
	return hashStrings(parts...)
}

func NormalizeReverseSSHErrorEvent(event ReverseSSHErrorEvent, receivedAt time.Time) (ReverseSSHErrorEvent, error) {
	event.Source = strings.TrimSpace(event.Source)
	if event.Source == "" {
		event.Source = "reverse_ssh"
	}
	event.Unit = strings.TrimSpace(event.Unit)
	rawReason := event.Reason
	event.Severity = normalizeErrorSeverity(event.Severity)
	event.Message = strings.TrimSpace(event.Message)
	event.RemoteAddr = strings.TrimSpace(event.RemoteAddr)
	event.RemoteIP = strings.TrimSpace(event.RemoteIP)
	event.Transport = strings.ToLower(strings.TrimSpace(event.Transport))
	event.Host = strings.TrimSpace(event.Host)
	event.Fingerprint = strings.TrimSpace(event.Fingerprint)
	event.RawLine = strings.TrimSpace(event.RawLine)
	if event.Message == "" && event.RawLine != "" {
		event.Message = event.RawLine
	}
	event.Reason = normalizeErrorReason(event.Reason, event.Message)
	if classifiedReason, classifiedSeverity, ok := ClassifyReverseSSHLogLine(event.Message); ok {
		normalizedRawReason := normalizeErrorReasonToken(rawReason)
		if normalizedRawReason == "" || normalizedRawReason == "generic_error" || event.Reason == classifiedReason {
			event.Reason = classifiedReason
			event.Severity = classifiedSeverity
		}
	}
	if event.Reason == "malformed_probe" {
		event.Severity = "info"
	}
	if event.Message == "" {
		return ReverseSSHErrorEvent{}, errors.New("message is required")
	}
	if event.ObservedAt.IsZero() {
		event.ObservedAt = receivedAt.UTC()
	} else {
		event.ObservedAt = event.ObservedAt.UTC()
	}
	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}
	event.ReceivedAt = receivedAt.UTC()
	if event.RemoteAddr != "" {
		event.RemoteIP, event.RemotePort = ParseEndpoint(event.RemoteAddr)
	} else if event.RemoteIP != "" && event.RemotePort > 0 {
		event.RemoteAddr = net.JoinHostPort(event.RemoteIP, strconv.Itoa(event.RemotePort))
	}
	if event.EventHash == "" {
		event.EventHash = HashReverseSSHErrorEvent(event)
	}
	return event, nil
}

func NewReverseSSHErrorEventFromLogLine(source, unit, line string, observedAt, receivedAt time.Time) (ReverseSSHErrorEvent, bool) {
	reason, severity, ok := ClassifyReverseSSHLogLine(line)
	if !ok {
		return ReverseSSHErrorEvent{}, false
	}
	event, err := NormalizeReverseSSHErrorEvent(ReverseSSHErrorEvent{
		Source:     source,
		Unit:       unit,
		Severity:   severity,
		Reason:     reason,
		Message:    line,
		RemoteAddr: FirstEndpointInText(line),
		ObservedAt: observedAt,
		RawLine:    line,
	}, receivedAt)
	if err != nil {
		return ReverseSSHErrorEvent{}, false
	}
	return event, true
}

func ClassifyReverseSSHLogLine(line string) (reason, severity string, ok bool) {
	text := strings.ToLower(strings.TrimSpace(line))
	if text == "" {
		return "", "", false
	}

	hasAny := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(text, value) {
				return true
			}
		}
		return false
	}
	hasFailureWord := hasAny("error", "fail", "failed", "failure", "invalid", "mismatch", "rejected", "denied", "refused", "timeout", "expired")

	switch {
	case strings.Contains(text, "fingerprint") && hasAny("mismatch", "does not match", "not match", "invalid", "unknown", "rejected"):
		return "fingerprint_mismatch", "error", true
	case hasAny("certificate", " cert ", "x509") && hasFailureWord:
		return "invalid_certificate", "error", true
	case hasAny("auth", "authenticate", "authorized key", "authorized_keys", "permission") && hasFailureWord:
		return "auth_failed", "error", true
	case hasAny("tls", "handshake") && hasFailureWord:
		return "handshake_failed", "error", true
	case hasAny("connect", "connection") && hasFailureWord:
		return "connection_failed", "error", true
	case strings.Contains(text, "multiplexing failed") && hasAny("unknown protocol", "failed to read header"):
		return "malformed_probe", "info", true
	case hasFailureWord:
		return "generic_error", "error", true
	default:
		return "", "", false
	}
}

func FirstEndpointInText(text string) string {
	for _, field := range strings.Fields(text) {
		candidate := strings.Trim(field, `"'(){}<>,;`)
		if candidate == "" {
			continue
		}
		host, port := ParseEndpoint(candidate)
		if host == "" || net.ParseIP(host) == nil {
			continue
		}
		if port > 0 {
			return candidate
		}
		return host
	}
	return ""
}

func HashReverseSSHErrorEvent(event ReverseSSHErrorEvent) string {
	observedAt := event.ObservedAt
	if observedAt.IsZero() {
		observedAt = event.ReceivedAt
	}
	return hashStrings(
		event.Source,
		event.Unit,
		event.Severity,
		event.Reason,
		event.Message,
		event.RemoteAddr,
		observedAt.UTC().Format(time.RFC3339Nano),
	)
}

func normalizeErrorSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "warning", "warn":
		return "warning"
	case "info":
		return "info"
	default:
		return "error"
	}
}

func normalizeErrorReason(reason, message string) string {
	reason = normalizeErrorReasonToken(reason)
	if reason != "" {
		return reason
	}
	if classified, _, ok := ClassifyReverseSSHLogLine(message); ok {
		return classified
	}
	return "generic_error"
}

func normalizeErrorReasonToken(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	reason = strings.ReplaceAll(reason, "-", "_")
	reason = strings.ReplaceAll(reason, " ", "_")
	return reason
}

func NewEnrichedEvent(event Event, ingress *IngressEvent, status string) EnrichedEvent {
	return NewEnrichedEventWithMethod(event, ingress, status, "")
}

func NewEnrichedEventWithMethod(event Event, ingress *IngressEvent, status, method string) EnrichedEvent {
	enriched := EnrichedEvent{
		SourceEventHash:      event.EventHash,
		CorrelationStatus:    strings.TrimSpace(status),
		CorrelationMethod:    strings.TrimSpace(method),
		Status:               event.Status,
		ReverseSSHID:         event.ReverseSSHID,
		HostName:             event.HostName,
		UserName:             event.UserName,
		ComputerName:         event.ComputerName,
		IPRaw:                event.IPRaw,
		IPAddr:               event.IPAddr,
		IPPort:               event.IPPort,
		Transport:            event.Transport,
		PublicKeyFingerprint: event.PublicKeyFingerprint,
		ProxySourceIP:        event.ProxySourceIP,
		Version:              event.Version,
		SourceTS:             event.SourceTS,
		ReceivedAt:           event.ReceivedAt.UTC(),
		RawWebhookJSON:       append([]byte(nil), event.RawJSON...),
	}
	if enriched.CorrelationStatus == "" {
		enriched.CorrelationStatus = "unmatched"
	}
	if ingress != nil {
		enriched.IngressEventHash = ingress.EventHash
		enriched.RealClientIP = ingress.ClientIP
		enriched.ClientPort = ingress.ClientPort
		enriched.Transport = ingress.Transport
		enriched.VPSName = ingress.VPSName
		enriched.VPSPublicIP = ingress.VPSPublicIP
		enriched.VPSInternalIP = ingress.VPSInternalIP
		enriched.ForwarderIP = ingress.ForwarderIP
		enriched.IngressReceivedAt = ingress.NginxReceivedAt.UTC()
		enriched.RawIngressJSON = append([]byte(nil), ingress.RawJSON...)
	}
	enriched.EventHash = HashEnrichedEvent(enriched)
	return enriched
}

func HashEnrichedEvent(event EnrichedEvent) string {
	return hashStrings(
		event.SourceEventHash,
		event.IngressEventHash,
		event.CorrelationStatus,
		event.CorrelationMethod,
		event.RealClientIP,
		event.Transport,
	)
}

func hashStrings(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
