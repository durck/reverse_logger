package rsshsession

import (
	"errors"
	"regexp"
	"strings"
)

type LiveSession struct {
	ReverseSSHID string `json:"reverse_ssh_id"`
	PublicKey    string `json:"public_key,omitempty"`
	HostName     string `json:"host_name,omitempty"`
	RemoteAddr   string `json:"remote_addr,omitempty"`
	Owners       string `json:"owners,omitempty"`
	Version      string `json:"version,omitempty"`
}

var (
	ErrEmptyListOutput = errors.New("empty reverse_ssh ls output")

	ansiPattern       = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	listLinePattern   = regexp.MustCompile(`^(\S+)\s+(\S+)\s+(\S+)\s+(\S+),\s+owners:\s+(.+?),\s+version:\s+(.+)$`)
	consolePromptLike = regexp.MustCompile(`^[A-Za-z0-9_.@-]+\$\s*(ls|exit)?\s*$`)
)

func ParseListOutput(output string) ([]LiveSession, error) {
	lines := strings.Split(output, "\n")
	sessions := make([]LiveSession, 0)
	var unclassified []string
	sawEmptyListing := false
	for _, line := range lines {
		line = normalizeConsoleLine(line)
		if isEmptyListingLine(line) {
			sawEmptyListing = true
			continue
		}
		if shouldIgnoreConsoleLine(line) {
			continue
		}
		match := listLinePattern.FindStringSubmatch(line)
		if len(match) == 0 {
			unclassified = append(unclassified, line)
			continue
		}
		sessions = append(sessions, LiveSession{
			ReverseSSHID: strings.TrimSpace(match[1]),
			PublicKey:    strings.TrimSpace(match[2]),
			HostName:     strings.TrimSpace(match[3]),
			RemoteAddr:   strings.TrimSpace(match[4]),
			Owners:       strings.TrimSpace(match[5]),
			Version:      strings.TrimSpace(match[6]),
		})
	}
	if len(unclassified) > 0 {
		return nil, errors.New("unrecognized reverse_ssh ls output line: " + unclassified[0])
	}
	if len(sessions) == 0 && !sawEmptyListing {
		return nil, ErrEmptyListOutput
	}
	return sessions, nil
}

func LiveSessionIDs(sessions []LiveSession) []string {
	seen := map[string]bool{}
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		id := strings.TrimSpace(session.ReverseSSHID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func normalizeConsoleLine(line string) string {
	line = strings.TrimSpace(strings.TrimRight(line, "\r"))
	line = ansiPattern.ReplaceAllString(line, "")
	line = strings.TrimSpace(strings.TrimRight(line, "\r"))
	return line
}

func shouldIgnoreConsoleLine(line string) bool {
	if line == "" {
		return true
	}
	lower := strings.ToLower(line)
	if line == "ls" || line == "exit" || consolePromptLike.MatchString(line) {
		return true
	}
	if strings.Contains(lower, "unknown command:") {
		return true
	}
	return false
}

func isEmptyListingLine(line string) bool {
	return strings.Contains(strings.ToLower(line), "no rssh clients connected")
}
