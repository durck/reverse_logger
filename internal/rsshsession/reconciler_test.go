package rsshsession

import (
	"bytes"
	"testing"
)

func TestWriteConsoleLineUsesCarriageReturn(t *testing.T) {
	var buf bytes.Buffer
	if err := writeConsoleLine(&buf, "ls"); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "ls\r" {
		t.Fatalf("console input = %q", got)
	}
}
