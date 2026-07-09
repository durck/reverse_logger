package rsshsession

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
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

func TestWaitForListOutputWaitsUntilParseable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var output lockedBuffer

	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = output.Write([]byte("catcher$ ls\r\n"))
		time.Sleep(20 * time.Millisecond)
		_, _ = output.Write([]byte("f8f1f59eab1730bb286de114a6981454e670d432 3ba18e89b52184a93821c51856c71e0a5e303fc1 tn.mamontovdk.zcr-img002-0633 92.246.76.17:60266, owners: public, version: SSH-windows_amd64\r\n"))
	}()

	if err := waitForListOutput(ctx, &output); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForListOutputRejectsPromptOnlyOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var output lockedBuffer
	_, _ = output.Write([]byte("catcher$ ls\r\n"))

	err := waitForListOutput(ctx, &output)
	if !errors.Is(err, ErrEmptyListOutput) {
		t.Fatalf("expected empty output error, got %v", err)
	}
}
