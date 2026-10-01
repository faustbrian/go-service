package service

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type maintenanceCountingReader struct {
	reader   io.Reader
	consumed int
}

func (reader *maintenanceCountingReader) Read(data []byte) (int, error) {
	n, err := reader.reader.Read(data)
	reader.consumed += n
	return n, err
}

func TestMaintenanceSnapshotReaderEnforcesBudgetBeforeRetention(t *testing.T) {
	for _, size := range []int{0, maximumMaintenanceStateBytes, maximumMaintenanceStateBytes + 1, maximumMaintenanceStateBytes + 32} {
		reader := &maintenanceCountingReader{reader: bytes.NewReader(bytes.Repeat([]byte(" "), size))}
		data, err := readMaintenanceSnapshot(reader)
		if size <= maximumMaintenanceStateBytes {
			if err != nil || len(data) != size || reader.consumed != size {
				t.Fatalf("size %d: retained=%d consumed=%d error=%v", size, len(data), reader.consumed, err)
			}
		} else {
			if err == nil || data != nil || reader.consumed != maximumMaintenanceStateBytes+1 {
				t.Fatalf("size %d: retained=%d consumed=%d error=%v", size, len(data), reader.consumed, err)
			}
		}
	}
}

type maintenanceFailingReader struct{ failure error }

func (reader maintenanceFailingReader) Read([]byte) (int, error) { return 0, reader.failure }

func TestMaintenanceSnapshotReaderPreservesReadFailureWithoutPartialSnapshot(t *testing.T) {
	want := errors.New("ordinary read failure")
	data, err := readMaintenanceSnapshot(maintenanceFailingReader{failure: want})
	if data != nil || !errors.Is(err, want) {
		t.Fatalf("partial snapshot=%v error=%v", data, err)
	}
}
