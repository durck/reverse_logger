package main

import (
	"testing"
	"time"
)

func TestLoadConfigParsesConsoleCommandDelay(t *testing.T) {
	t.Setenv("RSSH_SESSION_FORWARD_TOKEN", "token")
	t.Setenv("RSSH_SESSION_CONSOLE_KEY_PATH", "/run/key")
	t.Setenv("RSSH_SESSION_CONSOLE_COMMAND_DELAY", "1500ms")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Console.CommandDelay != 1500*time.Millisecond {
		t.Fatalf("command delay = %s", cfg.Console.CommandDelay)
	}
}
