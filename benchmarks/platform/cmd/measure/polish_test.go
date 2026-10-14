package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/faustbrian/go-service/benchmarks/platform/internal/measure"
)

func TestRSSMonitorRetainsPeakAndJoinsAfterStopOrReadFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		var calls atomic.Int32
		read := func() (int64, error) {
			call := calls.Add(1)
			if fail && call == 3 {
				return 0, errors.New("missing process")
			}
			return []int64{10, 40, 20}[min(call-1, 2)], nil
		}
		stop, err := startRSSMonitor(time.Millisecond, read)
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Second)
		for calls.Load() < 3 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		result := stop()
		if result.peak != 40 || result.readings < 2 || (result.err != nil) != fail {
			t.Fatalf("RSS result = %+v", result)
		}
		if again := stop(); again != result {
			t.Fatal("stop is not repeatable")
		}
		joinedCalls := calls.Load()
		time.Sleep(3 * time.Millisecond)
		if calls.Load() != joinedCalls {
			t.Fatal("sampler survived stop")
		}
	}
	if _, err := startRSSMonitor(time.Millisecond, func() (int64, error) { return 0, errors.New("unreadable") }); err == nil {
		t.Fatal("missing initial observation accepted")
	}
}

func TestSweepBoundsAndOrdering(t *testing.T) {
	for _, value := range []string{"", "1", "0,16", "16,1", "1,1", "1,257", "1,no", "1,2,3,4,5,6,7,8,9"} {
		if _, err := parseSweep(value); err == nil {
			t.Fatalf("unsafe sweep %q accepted", value)
		}
	}
	if got, err := parseSweep("1,16,64,256"); err != nil || len(got) != 4 {
		t.Fatalf("valid sweep = %v, %v", got, err)
	}
}

func TestCheckpointRejectsModifiedAdditionalMetric(t *testing.T) {
	directory := t.TempDir()
	raw := filepath.Join(directory, "raw")
	if err := os.Mkdir(raw, 0o750); err != nil {
		t.Fatal(err)
	}
	sample := checkpointSample(t, raw, "lowlevel-disabled-01")
	entry := preparedCandidate{item: candidate{command: "lowlevel"}, state: "disabled"}
	if err := validateCheckpointSample(raw, entry, 1, sample); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*measure.Sample){
		func(value *measure.Sample) { value.PeakRSSBytes++ },
		func(value *measure.Sample) { value.RSSReadings++ },
		func(value *measure.Sample) {
			value.Cancellation = []measure.Cancellation{{Mode: "disconnect", Observed: true}}
		},
		func(value *measure.Sample) { value.Saturation = []measure.SaturationStep{{Concurrency: 256}} },
		func(value *measure.Sample) { value.ObservationsRaw += "changed" },
	} {
		changed := sample
		mutate(&changed)
		if err := validateCheckpointSample(raw, entry, 1, changed); err == nil {
			t.Fatal("changed comparative metric accepted")
		}
	}
}

func TestBenchmarkDigestTracksSourceAndLocksNotReports(t *testing.T) {
	directory := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main")
	write("go.sum", "locks")
	first, err := benchmarkInputDigest(directory)
	if err != nil {
		t.Fatal(err)
	}
	write("README.md", "new prose")
	write("assertions_test.go", "new test assertions")
	second, err := benchmarkInputDigest(directory)
	if err != nil || second != first {
		t.Fatal("prose invalidated measurement inputs")
	}
	write("go.sum", "changed dependency")
	third, err := benchmarkInputDigest(directory)
	if err != nil || third == first {
		t.Fatal("changed dependency reused stale inputs")
	}
}

func TestResidentReadTimeoutBoundsSamplerCleanup(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "ps"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	started := time.Now()
	if _, err := residentBytes(os.Getpid()); err == nil {
		t.Fatal("stalled resident reader succeeded")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("resident read was not bounded: %s", elapsed)
	}
}
