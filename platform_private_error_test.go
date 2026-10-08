package service

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type nonComparableOutputError []error

func (nonComparableOutputError) Error() string       { return "private-output-failure" }
func (err nonComparableOutputError) Unwrap() []error { return []error(err) }

type callbackFailureWriter struct{ err error }

func (writer callbackFailureWriter) Write([]byte) (int, error) { return 0, writer.err }

func TestExecuteMaintenanceOutputFailureRetainsCLIClassification(t *testing.T) {
	t.Parallel()

	store := &commandMaintenanceStore{}
	failure := nonComparableOutputError{&StartupError{
		Component: "private-output-component",
		Err:       errors.New("private-output-cause"),
	}}
	var stderr bytes.Buffer
	exit := Execute(t.Context(), maintenanceCommandDefinition(store), Invocation{
		Args:   []string{"down"},
		Stdout: callbackFailureWriter{err: failure},
		Stderr: &stderr,
	})
	if exit != 1 {
		t.Fatalf("maintenance output failure exit = %d, want 1", exit)
	}
	if !store.state.Enabled {
		t.Fatal("maintenance transition was not published before the output failure")
	}
	if stderr.Len() == 0 {
		t.Fatal("output failure omitted its protected diagnostic")
	}
	for _, private := range []string{"private-output-failure", "private-output-component", "private-output-cause"} {
		if strings.Contains(stderr.String(), private) {
			t.Fatal("output failure disclosed private diagnostic content")
		}
	}
}
