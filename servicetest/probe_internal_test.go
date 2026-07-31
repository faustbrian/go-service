package servicetest

import (
	"bytes"
	"testing"
)

func TestProbeWriterNeverRetainsPastLimit(t *testing.T) {
	t.Parallel()

	writer := newProbeWriter(4)
	payload := bytes.Repeat([]byte("x"), 1<<20)
	written, err := writer.Write(payload)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if written != len(payload) {
		t.Fatalf("Write() = %d, want %d", written, len(payload))
	}
	if len(writer.body) != 4 || !writer.truncated {
		t.Fatalf("retained = %d, truncated = %v", len(writer.body), writer.truncated)
	}
	writer.WriteHeader(299)
	if writer.status != 200 {
		t.Fatalf("duplicate status = %d, want 200", writer.status)
	}
	fresh := newProbeWriter(0)
	fresh.WriteHeader(299)
	if fresh.status != 299 || fresh.Header() == nil {
		t.Fatalf("status = %d, header = %v", writer.status, writer.Header())
	}
	fresh.Flush()
}

func TestProbeWriterRetainsIncrementalWritesOnlyToItsExactLimit(t *testing.T) {
	t.Parallel()

	exact := newProbeWriter(4)
	if _, err := exact.Write([]byte("four")); err != nil {
		t.Fatalf("exact Write() error = %v", err)
	}
	if exact.truncated {
		t.Fatal("exact-limit write was marked truncated")
	}

	incremental := newProbeWriter(4)
	if _, err := incremental.Write([]byte("ab")); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	if _, err := incremental.Write([]byte("cdef")); err != nil {
		t.Fatalf("second Write() error = %v", err)
	}
	if got := string(incremental.body); got != "abcd" {
		t.Fatalf("incremental body = %q, want %q", got, "abcd")
	}
	if !incremental.truncated {
		t.Fatal("overflowing incremental write was not marked truncated")
	}
}
