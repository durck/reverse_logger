package rsshsession

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type ConsoleConfig struct {
	Host           string
	Port           int
	User           string
	PrivateKeyPath string
	KnownHostsPath string
	Timeout        time.Duration
	CommandDelay   time.Duration
}

type SnapshotClient struct {
	ForwardURL string
	Token      string
	HTTPClient *http.Client
}

type SnapshotPayload struct {
	CheckedAt         time.Time `json:"checked_at"`
	LiveReverseSSHIDs []string  `json:"live_reverse_ssh_ids"`
}

func FetchLiveSessions(ctx context.Context, config ConsoleConfig) ([]LiveSession, error) {
	output, err := RunConsoleCommand(ctx, config, "ls")
	if err != nil {
		return nil, err
	}
	return ParseListOutput(output)
}

func RunConsoleCommand(ctx context.Context, config ConsoleConfig, command string) (string, error) {
	config = normalizeConsoleConfig(config)
	if err := validateConsoleConfig(config); err != nil {
		return "", err
	}
	key, err := os.ReadFile(config.PrivateKeyPath)
	if err != nil {
		return "", fmt.Errorf("read console private key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return "", fmt.Errorf("parse console private key: %w", err)
	}
	if config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, config.Timeout)
		defer cancel()
	}

	address := net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return "", fmt.Errorf("dial reverse_ssh console: %w", err)
	}
	defer conn.Close()

	clientConn, chans, reqs, err := ssh.NewClientConn(conn, address, &ssh.ClientConfig{
		User:            config.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: knownHostCallback(config.KnownHostsPath, config.Host, config.Port),
		Timeout:         config.Timeout,
	})
	if err != nil {
		return "", fmt.Errorf("open reverse_ssh console SSH: %w", err)
	}
	client := ssh.NewClient(clientConn, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("start reverse_ssh console session: %w", err)
	}
	defer session.Close()

	var output bytes.Buffer
	session.Stdout = &output
	session.Stderr = &output
	stdin, err := session.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("open reverse_ssh console stdin: %w", err)
	}
	if err := session.RequestPty("xterm", 80, 40, ssh.TerminalModes{ssh.ECHO: 0}); err != nil {
		return "", fmt.Errorf("request reverse_ssh console pty: %w", err)
	}
	if err := session.Shell(); err != nil {
		return "", fmt.Errorf("start reverse_ssh console shell: %w", err)
	}

	command = strings.TrimSpace(command)
	if command == "" {
		command = "ls"
	}
	if err := waitConsoleDelay(ctx, config.CommandDelay); err != nil {
		_ = session.Close()
		return output.String(), fmt.Errorf("wait reverse_ssh console prompt: %w", err)
	}
	if err := writeConsoleLine(stdin, command); err != nil {
		_ = session.Close()
		return output.String(), fmt.Errorf("write reverse_ssh console command: %w", err)
	}
	if err := waitConsoleDelay(ctx, config.CommandDelay); err != nil {
		_ = session.Close()
		return output.String(), fmt.Errorf("wait reverse_ssh console command output: %w", err)
	}
	_ = writeConsoleLine(stdin, "exit")
	_ = stdin.Close()

	done := make(chan error, 1)
	go func() {
		done <- session.Wait()
	}()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) {
			return output.String(), fmt.Errorf("reverse_ssh console command failed: %w", err)
		}
	case <-ctx.Done():
		_ = session.Close()
		_ = client.Close()
		return output.String(), ctx.Err()
	}
	return output.String(), nil
}

func (c SnapshotClient) PostSnapshot(ctx context.Context, payload SnapshotPayload) error {
	if strings.TrimSpace(c.Token) == "" {
		return errors.New("snapshot token is required")
	}
	endpoint, err := snapshotEndpoint(c.ForwardURL, c.Token)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.Token))
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("snapshot post failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	return nil
}

func WaitForLogger(ctx context.Context, forwardURL string, client *http.Client) error {
	healthURL, err := healthEndpoint(forwardURL)
	if err != nil {
		return err
	}
	if client == nil {
		client = http.DefaultClient
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err == nil {
			resp, err := client.Do(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func normalizeConsoleConfig(config ConsoleConfig) ConsoleConfig {
	config.Host = strings.TrimSpace(config.Host)
	if config.Host == "" {
		config.Host = "reverse_ssh"
	}
	if config.Port <= 0 {
		config.Port = 2222
	}
	config.User = strings.TrimSpace(config.User)
	if config.User == "" {
		config.User = "root"
	}
	config.PrivateKeyPath = strings.TrimSpace(config.PrivateKeyPath)
	config.KnownHostsPath = strings.TrimSpace(config.KnownHostsPath)
	if config.KnownHostsPath == "" {
		config.KnownHostsPath = "/state/known_hosts"
	}
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Second
	}
	if config.CommandDelay <= 0 {
		config.CommandDelay = time.Second
	}
	return config
}

func validateConsoleConfig(config ConsoleConfig) error {
	if config.PrivateKeyPath == "" {
		return errors.New("console private key path is required")
	}
	return nil
}

func knownHostCallback(path, host string, port int) ssh.HostKeyCallback {
	path = strings.TrimSpace(path)
	if path == "" {
		return ssh.InsecureIgnoreHostKey()
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		callback, err := knownhosts.New(path)
		if err == nil {
			err = callback(hostname, remote, key)
			if err == nil {
				return nil
			}
			var keyErr *knownhosts.KeyError
			if !errors.As(err, &keyErr) || len(keyErr.Want) > 0 {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return appendKnownHost(path, host, port, key)
	}
}

func appendKnownHost(path, host string, port int, key ssh.PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	line := knownhosts.Line([]string{knownHostPattern(host, port)}, key)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, line)
	return err
}

func knownHostPattern(host string, port int) string {
	host = strings.Trim(host, "[]")
	if port == 22 {
		return host
	}
	return "[" + host + "]:" + strconv.Itoa(port)
}

func waitConsoleDelay(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func writeConsoleLine(w io.Writer, line string) error {
	_, err := io.WriteString(w, line+"\r")
	return err
}

func snapshotEndpoint(rawURL, token string) (string, error) {
	rawURL = strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if rawURL == "" {
		rawURL = "http://rssh-logger:8080/session-snapshots"
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("snapshot forward URL must be absolute")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + url.PathEscape(strings.TrimSpace(token))
	parsed.RawQuery = ""
	return parsed.String(), nil
}

func healthEndpoint(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		rawURL = "http://rssh-logger:8080/session-snapshots"
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("snapshot forward URL must be absolute")
	}
	parsed.Path = "/healthz"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
