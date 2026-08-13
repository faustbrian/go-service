package main

import (
	"path/filepath"
	"testing"

	referencedurability "github.com/faustbrian/golib/pkg/service/integration/reference-durability"
)

func TestExpectationRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "expectation.json")
	want := referencedurability.RecoveryExpectation{
		EnvelopeID: "envelope-1", TaskID: "envelope-1", TaskKey: "reference-command-1",
	}
	if err := writeExpectation(path, want); err != nil {
		t.Fatalf("writeExpectation() error = %v", err)
	}
	got, err := readExpectation(path)
	if err != nil {
		t.Fatalf("readExpectation() error = %v", err)
	}
	if got != want {
		t.Fatalf("readExpectation() = %#v, want %#v", got, want)
	}
}

func TestRunRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	if err := run(nil); err == nil {
		t.Fatal("run(nil) error = nil")
	}
	if err := run([]string{"-mode", "unknown", "-expectation", "unused"}); err == nil {
		t.Fatal("run(unknown mode) error = nil")
	}
}
