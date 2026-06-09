package events

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
)

type Event struct {
	EventHash    string          `json:"event_hash"`
	Status       string          `json:"status"`
	ReverseSSHID string          `json:"reverse_ssh_id"`
	HostName     string          `json:"host_name"`
	UserName     string          `json:"user_name,omitempty"`
	ComputerName string          `json:"computer_name,omitempty"`
	IPRaw        string          `json:"ip_raw,omitempty"`
	IPAddr       string          `json:"ip_addr,omitempty"`
	IPPort       int             `json:"ip_port,omitempty"`
	Version      string          `json:"version,omitempty"`
	SourceTS     time.Time       `json:"source_ts,omitempty"`
	ReceivedAt   time.Time       `json:"received_at"`
	RawJSON      json.RawMessage `json:"raw_json,omitempty"`
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

type clientState struct {
	Status    string    `json:"Status"`
	ID        string    `json:"ID"`
	IP        string    `json:"IP"`
	HostName  string    `json:"HostName"`
	Version   string    `json:"Version"`
	Timestamp time.Time `json:"Timestamp"`
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
	event := Event{
		Status:       strings.ToLower(strings.TrimSpace(state.Status)),
		ReverseSSHID: strings.TrimSpace(state.ID),
		HostName:     strings.TrimSpace(state.HostName),
		UserName:     userName,
		ComputerName: computerName,
		IPRaw:        strings.TrimSpace(state.IP),
		IPAddr:       ipAddr,
		IPPort:       ipPort,
		Version:      strings.TrimSpace(state.Version),
		SourceTS:     state.Timestamp,
		ReceivedAt:   receivedAt.UTC(),
		RawJSON:      raw,
	}
	event.EventHash = HashEvent(event)
	return event
}

func validateEvent(event Event) error {
	if event.Status == "" {
		return errors.New("Status is required")
	}
	if event.ReverseSSHID == "" {
		return errors.New("ID is required")
	}
	if event.HostName == "" {
		return errors.New("HostName is required")
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

func hashStrings(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
