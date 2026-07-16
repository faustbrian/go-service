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
