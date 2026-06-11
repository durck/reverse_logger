package main

import (
	"os"
	"testing"
)

func TestNormalizeTailOffsetStartsAtEnd(t *testing.T) {
	file := tempLogFile(t, "abcdef")
	defer file.Close()

	offset, err := normalizeTailOffset(file, -1)
	if err != nil {
		t.Fatal(err)
	}
	if offset != 6 {
		t.Fatalf("offset = %d, want 6", offset)
	}
}

func TestNormalizeTailOffsetKeepsValidOffset(t *testing.T) {
	file := tempLogFile(t, "abcdef")
	defer file.Close()

	offset, err := normalizeTailOffset(file, 3)
	if err != nil {
		t.Fatal(err)
	}
	if offset != 3 {
		t.Fatalf("offset = %d, want 3", offset)
	}
}

func TestNormalizeTailOffsetResetsAfterTruncate(t *testing.T) {
	file := tempLogFile(t, "abc")
	defer file.Close()

	offset, err := normalizeTailOffset(file, 100)
	if err != nil {
		t.Fatal(err)
	}
	if offset != 0 {
		t.Fatalf("offset = %d, want 0", offset)
	}
}

func tempLogFile(t *testing.T, content string) *os.File {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), "nginx-edge-*.log")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	return file
}
