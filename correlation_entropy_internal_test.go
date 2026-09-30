package service

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/faustbrian/go-cli"
	"github.com/faustbrian/go-correlation"
)

const entropyHelperEnv = "GOLIB_SERVICE_ENTROPY_HELPER"

type failingEntropyReader struct{}

func (failingEntropyReader) Read([]byte) (int, error) {
	return 0, errors.New("private entropy marker")
}

func TestDefaultCorrelationFailureDoesNotExposeEntropyDetail(t *testing.T) {
	if os.Getenv(entropyHelperEnv) != "1" {
		command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestDefaultCorrelationFailureDoesNotExposeEntropyDetail$")
		command.Env = append(os.Environ(), entropyHelperEnv+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated service command failed: %s", output)
		}
		return
	}

	cryptorand.Reader = failingEntropyReader{}
	command := CommandFor(CommandSpec[struct{}]{
		Name: "migrate",
		Kind: CommandKindOneShot,
		Load: func(context.Context, Invocation) (struct{}, error) {
			return struct{}{}, errors.New("configuration reached after entropy failure")
		},
		Build: func(context.Context, BuildContext, struct{}) (Plan, error) {
			return Plan{}, nil
		},
	})
	application, _, err := compileDefinition(Definition{
		Identity: Identity{Name: "postal"},
		Commands: Commands{Migrate: command},
	}, Invocation{Args: []string{"migrate"}, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal("service command construction failed")
	}
	result := application.RunCommand(context.Background(), cli.Request{
		Args: []string{"migrate"}, Stdout: io.Discard, Stderr: io.Discard,
		NonInteractive: true,
	})
	if !errors.Is(result.Err, correlation.ErrGeneration) {
		t.Fatal("default correlation failure lost its classification")
	}
	var construction *ConstructionError
	if !errors.As(result.Err, &construction) {
		t.Fatal("default correlation failure lost its construction boundary")
	}
	if strings.Contains(construction.Err.Error(), "private entropy marker") {
		t.Fatal("underlying entropy detail reached the service construction error")
	}
}
