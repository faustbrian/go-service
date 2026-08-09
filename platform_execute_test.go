package service_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/cli"
	"github.com/faustbrian/golib/pkg/correlation"
	"github.com/faustbrian/golib/pkg/service"
	"github.com/faustbrian/golib/pkg/service/serverhttp"
)

type closeObservedListener struct {
	net.Listener

	closed chan struct{}
	once   sync.Once
}

func (listener *closeObservedListener) Close() error {
	listener.once.Do(func() { close(listener.closed) })

	return listener.Listener.Close()
}

func TestMainRejectsAnInvalidDefinition(t *testing.T) {
	if exit := service.Main(service.Definition{}); exit != 70 {
		t.Fatalf("Main() exit = %d, want 70", exit)
	}
}

func TestExecuteRejectsInvalidInvocationBoundary(t *testing.T) {
	t.Parallel()

	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			t.Fatal("Load() called across an invalid invocation boundary")

			return struct{}{}, nil
		},
		Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
			return service.Plan{}, nil
		},
	})
	definition := service.Definition{
		Identity: service.Identity{Name: "postal"},
		Commands: service.Commands{Migrate: command},
	}
	tests := []struct {
		ctx        context.Context
		invocation service.Invocation
	}{
		{
			ctx: nil,
			invocation: service.Invocation{
				Args:   []string{"migrate"},
				Stdout: io.Discard,
				Stderr: io.Discard,
			},
		},
		{
			ctx: context.Background(),
			invocation: service.Invocation{
				Args:   []string{"migrate"},
				Stderr: io.Discard,
			},
		},
		{
			ctx: context.Background(),
			invocation: service.Invocation{
				Args:   []string{"migrate"},
				Stdout: io.Discard,
			},
		},
	}
	for _, test := range tests {
		if exit := service.Execute(
			test.ctx,
			definition,
			test.invocation,
		); exit != 70 {
			t.Fatalf("Execute() exit = %d, want 70", exit)
		}
	}
}

func TestExecuteDoesNotTreatPreselectionCancellationAsSuccessfulShutdown(t *testing.T) {
	t.Parallel()

	loaded := false
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			loaded = true

			return struct{}{}, nil
		},
		Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
			return service.Plan{}, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exit := service.Execute(ctx, service.Definition{
		Identity: service.Identity{Name: "postal"},
		Commands: service.Commands{Migrate: command},
	}, service.Invocation{
		Args:   []string{"migrate"},
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if exit != 70 {
		t.Fatalf("Execute() exit = %d, want preselection failure exit 70", exit)
	}
	if loaded {
		t.Fatal("Load() called after cancellation before command selection")
	}
}

func TestExecuteAcceptsMoreCustomCommandsThanStandardSlots(t *testing.T) {
	t.Parallel()

	names := []string{"audit", "compact", "export", "inspect", "repair"}
	custom := make([]service.Command, 0, len(names))
	for _, name := range names {
		custom = append(custom, service.CommandFor(service.CommandSpec[struct{}]{
			Name: name,
			Kind: service.CommandKindOneShot,
			Load: func(context.Context, service.Invocation) (struct{}, error) {
				return struct{}{}, nil
			},
			Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
				return service.Plan{}, nil
			},
		}))
	}

	if exit := service.Execute(context.Background(), service.Definition{
		Identity: service.Identity{Name: "postal"},
		Commands: service.Commands{Custom: custom},
	}, service.Invocation{
		Args:   []string{"--help"},
		Stdout: io.Discard,
		Stderr: io.Discard,
	}); exit != 0 {
		t.Fatalf("Execute() exit = %d, want 0", exit)
	}
}

func TestExecuteAcceptsDeclaredCommandOptionsBeforeLoadingConfiguration(t *testing.T) {
	t.Parallel()

	date := cli.StringOption("date").Description("business date")
	var received []string
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate", Summary: "run a bounded operation",
		Kind:    service.CommandKindOneShot,
		Options: []cli.OptionDefinition{date},
		Load: func(_ context.Context, invocation service.Invocation) (struct{}, error) {
			received = append([]string(nil), invocation.Args...)
			return struct{}{}, nil
		},
		Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
			return service.Plan{}, nil
		},
	})
	exit := executeTest(t, context.Background(), service.Definition{
		Identity: service.Identity{Name: "postal"},
		Commands: service.Commands{Migrate: command},
	}, service.Invocation{
		Args:   []string{"migrate", "--date=2026-07-28"},
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if exit != 0 {
		t.Fatalf("Execute() exit = %d, want zero", exit)
	}
	if !reflect.DeepEqual(received, []string{"migrate", "--date=2026-07-28"}) {
		t.Fatalf("Load() args = %q", received)
	}
}

func TestExecuteRejectsInvalidDeclaredCommandOptions(t *testing.T) {
	t.Parallel()

	loaded := false
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate", Summary: "run a bounded operation",
		Kind:    service.CommandKindOneShot,
		Options: []cli.OptionDefinition{nil},
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			loaded = true
			return struct{}{}, nil
		},
		Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
			return service.Plan{}, nil
		},
	})
	exit := executeTest(t, context.Background(), service.Definition{
		Identity: service.Identity{Name: "postal"},
		Commands: service.Commands{Migrate: command},
	}, service.Invocation{
		Args: []string{"migrate"}, Stdout: io.Discard, Stderr: io.Discard,
	})
	if exit != 70 || loaded {
		t.Fatalf("Execute() exit = %d, loaded = %v", exit, loaded)
	}
}

func TestExecuteRunsOnlyTheSelectedOneShotCommand(t *testing.T) {
	t.Parallel()

	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		t.Fatalf("correlation.NewFactory() error = %v", err)
	}

	var events []string
	migrate := service.CommandFor(service.CommandSpec[string]{
		Name:    "migrate",
		Summary: "run database migrations",
		Kind:    service.CommandKindOneShot,
		Load: func(ctx context.Context, invocation service.Invocation) (string, error) {
			assertCorrelationContext(t, ctx)
			events = append(events, "load")

			return invocation.Environment[0], nil
		},
		Build: func(
			ctx context.Context,
			build service.BuildContext,
			configuration string,
		) (service.Plan, error) {
			assertCorrelationContext(t, ctx)
			if build.Identity.Name != "postal" || build.Identity.Role != "migrate" {
				t.Fatalf("process identity = %#v", build.Identity)
			}
			if build.Correlation != factory {
				t.Fatal("build correlation factory was replaced")
			}
			events = append(events, "build "+configuration)

			return service.Plan{
				Components: []service.Component{{
					Name: "database",
					Start: func(context.Context) error {
						events = append(events, "start database")

						return nil
					},
					Stop: func(context.Context) error {
						events = append(events, "stop database")

						return nil
					},
				}},
				Tasks: []service.Task{{
					Name: "migrate",
					Run: func(ctx context.Context) error {
						assertCorrelationContext(t, ctx)
						events = append(events, "run migration")

						return nil
					},
				}},
			}, nil
		},
	})
	workerLoaded := false
	worker := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "worker",
		Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			workerLoaded = true

			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{}, nil
		},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exit := executeTest(t, context.Background(), service.Definition{
		Identity:    service.Identity{Name: "postal"},
		Commands:    service.Commands{Worker: worker, Migrate: migrate},
		Correlation: factory,
	}, service.Invocation{
		Args:        []string{"migrate"},
		Environment: []string{"DATABASE_URL=redacted"},
		Stdout:      &stdout,
		Stderr:      &stderr,
	})

	if exit != 0 {
		t.Fatalf("Execute() exit = %d, stderr = %q", exit, stderr.String())
	}
	if workerLoaded {
		t.Fatal("unselected worker loaded configuration")
	}
	want := []string{
		"load",
		"build DATABASE_URL=redacted",
		"start database",
		"run migration",
		"stop database",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestExecuteSnapshotsInvocationSlices(t *testing.T) {
	t.Parallel()

	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(
			_ context.Context,
			invocation service.Invocation,
		) (struct{}, error) {
			invocation.Args[0] = "changed"
			invocation.Environment[0] = "CHANGED=true"

			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{}, nil
		},
	})
	args := []string{"migrate"}
	environment := []string{"LOCAL=true"}
	exit := executeTest(t, context.Background(), service.Definition{
		Identity: service.Identity{Name: "postal"},
		Commands: service.Commands{Migrate: command},
	}, service.Invocation{
		Args:        args,
		Environment: environment,
		Stdout:      io.Discard,
		Stderr:      io.Discard,
	})

	if exit != 0 {
		t.Fatalf("Execute() exit = %d, want 0", exit)
	}
	if args[0] != "migrate" || environment[0] != "LOCAL=true" {
		t.Fatalf("caller slices changed to %q and %q", args[0], environment[0])
	}
}

func TestExecuteBoundsConstructionWithoutBoundingRuntimeWork(t *testing.T) {
	t.Parallel()

	loadDeadline := false
	buildDeadline := false
	taskDeadline := true
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(ctx context.Context, _ service.Invocation) (struct{}, error) {
			_, loadDeadline = ctx.Deadline()

			return struct{}{}, nil
		},
		Build: func(
			ctx context.Context,
			_ service.BuildContext,
			_ struct{},
		) (service.Plan, error) {
			_, buildDeadline = ctx.Deadline()

			return service.Plan{Tasks: []service.Task{{
				Name: "migration",
				Run: func(ctx context.Context) error {
					_, taskDeadline = ctx.Deadline()

					return nil
				},
			}}}, nil
		},
	})
	exit := executeTest(t, context.Background(), service.Definition{
		Identity: service.Identity{Name: "postal"},
		Commands: service.Commands{Migrate: command},
	}, service.Invocation{
		Args:   []string{"migrate"},
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if exit != 0 {
		t.Fatalf("Execute() exit = %d, want 0", exit)
	}
	if !loadDeadline || !buildDeadline {
		t.Fatalf("construction deadlines = load %v, build %v", loadDeadline, buildDeadline)
	}
	if taskDeadline {
		t.Fatal("finite task inherited the construction deadline")
	}
}

func TestExecuteRejectsInvalidDefinitionBeforeCommandParsing(t *testing.T) {
	t.Parallel()

	invalid := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "worker",
		Kind: service.CommandKindLongRunning,
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{}, nil
		},
	})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exit := executeTest(t, context.Background(), service.Definition{
		Identity: service.Identity{Name: "postal", Version: "1.2.3"},
		Commands: service.Commands{Worker: invalid},
	}, service.Invocation{
		Args:   []string{"--version"},
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if exit != 70 {
		t.Fatalf(
			"Execute() exit = %d, stdout = %q, stderr = %q; want 70",
			exit,
			stdout.String(),
			stderr.String(),
		)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "requires load and build callbacks") {
		t.Fatalf("stderr = %q, want callback validation", stderr.String())
	}
}

func TestExecuteRejectsInvalidCommandRegistrations(t *testing.T) {
	t.Parallel()

	valid := func(name string, kind service.CommandKind) service.Command {
		return service.CommandFor(service.CommandSpec[struct{}]{
			Name: name,
			Kind: kind,
			Load: func(context.Context, service.Invocation) (struct{}, error) {
				return struct{}{}, nil
			},
			Build: func(
				context.Context,
				service.BuildContext,
				struct{},
			) (service.Plan, error) {
				return service.Plan{}, nil
			},
		})
	}
	tests := map[string]service.Definition{
		"no commands": {
			Identity: service.Identity{Name: "postal"},
		},
		"standard slot name mismatch": {
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Serve: valid(
				"worker",
				service.CommandKindLongRunning,
			)},
		},
		"serve is one shot": {
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Serve: valid(
				"serve",
				service.CommandKindOneShot,
			)},
		},
		"migrate is long running": {
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Migrate: valid(
				"migrate",
				service.CommandKindLongRunning,
			)},
		},
		"empty custom command": {
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Custom: []service.Command{{}}},
		},
		"reserved custom command": {
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Custom: []service.Command{
				valid("serve", service.CommandKindLongRunning),
			}},
		},
		"duplicate custom command": {
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Custom: []service.Command{
				valid("private-token", service.CommandKindOneShot),
				valid("private-token", service.CommandKindOneShot),
			}},
		},
		"malformed custom name": {
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Custom: []service.Command{
				valid("Inspect", service.CommandKindOneShot),
			}},
		},
		"unknown command kind": {
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Custom: []service.Command{
				valid("inspect", service.CommandKind(255)),
			}},
		},
	}
	for name, definition := range tests {
		var stderr bytes.Buffer
		exit := executeTest(t, context.Background(), definition, service.Invocation{
			Args:   []string{"--help"},
			Stdout: io.Discard,
			Stderr: &stderr,
		})
		if exit != 70 {
			t.Fatalf("%s exit = %d, want 70", name, exit)
		}
		if strings.Contains(stderr.String(), "private-token") {
			t.Fatalf("%s stderr disclosed command metadata: %q", name, stderr.String())
		}
	}
}

func TestExecuteRejectsCommandMetadataBeyondCLILimits(t *testing.T) {
	t.Parallel()

	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name:    "migrate",
		Summary: strings.Repeat("x", (1<<20)+1),
		Kind:    service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{}, nil
		},
	})
	exit := executeTest(t, context.Background(), service.Definition{
		Identity: service.Identity{Name: "postal"},
		Commands: service.Commands{Migrate: command},
	}, service.Invocation{
		Args:   []string{"--help"},
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if exit != 70 {
		t.Fatalf("Execute() exit = %d, want 70", exit)
	}
}

func TestExecuteValidatesPresentBuildMetadataBeforeLoading(t *testing.T) {
	t.Parallel()

	loaded := false
	migrate := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			loaded = true

			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{}, nil
		},
	})
	identities := []service.Identity{
		{Name: "Postal"},
		{Name: "postal", Version: "latest"},
		{Name: "postal", Version: "1.0.0-01"},
		{Name: "postal", Commit: "not a revision"},
		{Name: "postal", BuildTime: "yesterday"},
	}
	for _, identity := range identities {
		var stderr bytes.Buffer
		exit := executeTest(t, context.Background(), service.Definition{
			Identity: identity,
			Commands: service.Commands{Migrate: migrate},
		}, service.Invocation{
			Args:   []string{"migrate"},
			Stdout: io.Discard,
			Stderr: &stderr,
		})

		if exit != 70 {
			t.Fatalf(
				"Execute(%#v) exit = %d, stderr = %q; want 70",
				identity,
				exit,
				stderr.String(),
			)
		}
	}
	if loaded {
		t.Fatal("configuration loaded before identity validation")
	}
}

func TestExecuteAcceptsPlainSemanticVersion(t *testing.T) {
	t.Parallel()

	migrate := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{}, nil
		},
	})
	exit := executeTest(t, context.Background(), service.Definition{
		Identity: service.Identity{Name: "postal", Version: "1.2.3"},
		Commands: service.Commands{Migrate: migrate},
	}, service.Invocation{
		Args:    []string{"migrate"},
		Signals: make(chan os.Signal),
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	})
	if exit != 0 {
		t.Fatalf("Execute() exit = %d, want 0", exit)
	}
}

func TestExecuteComposesCLIHelpVersionAndUsageWithoutLoading(t *testing.T) {
	t.Parallel()

	loaded := false
	migrate := service.CommandFor(service.CommandSpec[struct{}]{
		Name:    "migrate",
		Summary: "run database migrations",
		Kind:    service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			loaded = true

			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{}, nil
		},
	})
	definition := service.Definition{
		Identity: service.Identity{
			Name: "postal", Version: "1.2.3-alpha.1+build.5",
		},
		Commands: service.Commands{Migrate: migrate},
	}
	tests := []struct {
		name       string
		args       []string
		exit       int
		wantStdout string
		wantStderr string
	}{
		{
			name: "help", args: []string{"--help"}, exit: 0,
			wantStdout: "run database migrations",
		},
		{
			name: "version", args: []string{"--version"}, exit: 0,
			wantStdout: "postal 1.2.3-alpha.1+build.5",
		},
		{
			name: "unknown", args: []string{"unknown"}, exit: 2,
			wantStderr: "unknown",
		},
	}
	for _, test := range tests {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		exit := executeTest(t, context.Background(), definition, service.Invocation{
			Args:   test.args,
			Stdout: &stdout,
			Stderr: &stderr,
		})
		if exit != test.exit {
			t.Fatalf("%s exit = %d, want %d", test.name, exit, test.exit)
		}
		if !strings.Contains(stdout.String(), test.wantStdout) {
			t.Fatalf("%s stdout = %q, want %q", test.name, stdout.String(), test.wantStdout)
		}
		if !strings.Contains(stderr.String(), test.wantStderr) {
			t.Fatalf("%s stderr = %q, want %q", test.name, stderr.String(), test.wantStderr)
		}
	}
	if loaded {
		t.Fatal("help, version, or usage loaded command configuration")
	}
}

func TestConfigurationFailureUsesExit78WithoutDisclosingValues(t *testing.T) {
	t.Parallel()

	migrate := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, errors.New(
				"DATABASE_URL=postgres://admin:secret@example.invalid/database",
			)
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{}, nil
		},
	})
	var stderr bytes.Buffer
	exit := executeTest(t, context.Background(), service.Definition{
		Identity: service.Identity{Name: "postal"},
		Commands: service.Commands{Migrate: migrate},
	}, service.Invocation{
		Args:   []string{"migrate"},
		Stdout: io.Discard,
		Stderr: &stderr,
	})

	if exit != 78 {
		t.Fatalf("Execute() exit = %d, stderr = %q; want 78", exit, stderr.String())
	}
	if strings.Contains(stderr.String(), "DATABASE_URL") ||
		strings.Contains(stderr.String(), "secret") {
		t.Fatalf("stderr disclosed configuration value: %q", stderr.String())
	}
}

func TestConfigurationAndConstructionPanicsUseSafeClassifiedExits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		load  func(context.Context, service.Invocation) (struct{}, error)
		build func(context.Context, service.BuildContext, struct{}) (service.Plan, error)
		exit  int
	}{
		{
			name: "configuration panic",
			load: func(context.Context, service.Invocation) (struct{}, error) {
				panic("configuration secret")
			},
			build: func(
				context.Context,
				service.BuildContext,
				struct{},
			) (service.Plan, error) {
				return service.Plan{}, nil
			},
			exit: 78,
		},
		{
			name: "construction panic",
			load: func(context.Context, service.Invocation) (struct{}, error) {
				return struct{}{}, nil
			},
			build: func(
				context.Context,
				service.BuildContext,
				struct{},
			) (service.Plan, error) {
				panic("construction secret")
			},
			exit: 70,
		},
	}
	for _, test := range tests {
		command := service.CommandFor(service.CommandSpec[struct{}]{
			Name:  "migrate",
			Kind:  service.CommandKindOneShot,
			Load:  test.load,
			Build: test.build,
		})
		var stderr bytes.Buffer
		exit := executeTest(t, context.Background(), service.Definition{
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Migrate: command},
		}, service.Invocation{
			Args:   []string{"migrate"},
			Stdout: io.Discard,
			Stderr: &stderr,
		})
		if exit != test.exit {
			t.Fatalf("%s exit = %d, want %d", test.name, exit, test.exit)
		}
		if strings.Contains(stderr.String(), "secret") {
			t.Fatalf("%s stderr disclosed panic: %q", test.name, stderr.String())
		}
	}
}

func TestConstructionAndCorrelationFailuresUseSafeExit70(t *testing.T) {
	t.Parallel()

	buildFailure := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{}, errors.New("constructor token=secret")
		},
	})
	failingFactory, err := correlation.NewFactory(correlation.FactoryOptions{
		Generator: correlation.GeneratorFunc(func() (string, error) {
			return "", errors.New("random source failed with secret")
		}),
	})
	if err != nil {
		t.Fatalf("correlation.NewFactory() error = %v", err)
	}
	correlationFailure := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			t.Fatal("configuration loaded after correlation generation failed")

			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{}, nil
		},
	})
	tests := []struct {
		name        string
		command     service.Command
		correlation *correlation.Factory
	}{
		{name: "construction", command: buildFailure},
		{
			name: "correlation", command: correlationFailure,
			correlation: failingFactory,
		},
	}
	for _, test := range tests {
		var stderr bytes.Buffer
		exit := executeTest(t, context.Background(), service.Definition{
			Identity:    service.Identity{Name: "postal"},
			Commands:    service.Commands{Migrate: test.command},
			Correlation: test.correlation,
		}, service.Invocation{
			Args:   []string{"migrate"},
			Stdout: io.Discard,
			Stderr: &stderr,
		})
		if exit != 70 {
			t.Fatalf("%s exit = %d, stderr = %q", test.name, exit, stderr.String())
		}
		if strings.Contains(stderr.String(), "secret") {
			t.Fatalf("%s disclosed a secret: %q", test.name, stderr.String())
		}
	}
}

func TestComponentStartupFailureUsesExit75(t *testing.T) {
	t.Parallel()

	migrate := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{Components: []service.Component{{
				Name: "database",
				Start: func(context.Context) error {
					return errors.New("postgres://admin:secret@example.invalid")
				},
			}}}, nil
		},
	})
	var stderr bytes.Buffer
	exit := executeTest(t, context.Background(), service.Definition{
		Identity: service.Identity{Name: "postal"},
		Commands: service.Commands{Migrate: migrate},
	}, service.Invocation{
		Args:   []string{"migrate"},
		Stdout: io.Discard,
		Stderr: &stderr,
	})

	if exit != 75 {
		t.Fatalf("Execute() exit = %d, stderr = %q; want 75", exit, stderr.String())
	}
	if strings.Contains(stderr.String(), "secret") {
		t.Fatalf("stderr disclosed startup failure: %q", stderr.String())
	}
}

func TestOneShotCommandCanExplicitlyOptIntoManagement(t *testing.T) {
	t.Parallel()

	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("management net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = management.Close() })
	taskStarted := make(chan struct{})
	releaseTask := make(chan struct{})
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{
				Management: true,
				Tasks: []service.Task{{
					Name: "migrate",
					Run: func(context.Context) error {
						close(taskStarted)
						<-releaseTask

						return nil
					},
				}},
			}, nil
		},
	})
	var stderr bytes.Buffer
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(context.Background(), service.Definition{
			Identity:   service.Identity{Name: "postal"},
			Commands:   service.Commands{Migrate: command},
			Management: service.Management{Listener: management},
		}, service.Invocation{
			Args:   []string{"migrate"},
			Stdout: io.Discard,
			Stderr: &stderr,
		})
	}()
	select {
	case <-taskStarted:
	case exit := <-result:
		t.Fatalf("Execute() exited before task start with %d: %s", exit, stderr.String())
	}

	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + management.Addr().String() + "/livez")
	if err != nil {
		t.Fatalf("GET /livez error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		t.Fatalf("GET /livez status = %d", response.StatusCode)
	}
	_ = response.Body.Close()
	close(releaseTask)
	if exit := receiveTestValue(t, result); exit != 0 {
		t.Fatalf("Execute() exit = %d, stderr = %q; want 0", exit, stderr.String())
	}
}

func TestInvalidTaskIsRejectedBeforeComponentOwnership(t *testing.T) {
	t.Parallel()

	componentStarted := false
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{
				Components: []service.Component{{
					Name: "database",
					Start: func(context.Context) error {
						componentStarted = true

						return nil
					},
				}},
				Tasks: []service.Task{{Name: "invalid"}},
			}, nil
		},
	})
	exit := executeTest(t, context.Background(), service.Definition{
		Identity: service.Identity{Name: "postal"},
		Commands: service.Commands{Migrate: command},
	}, service.Invocation{
		Args:   []string{"migrate"},
		Stdout: io.Discard,
		Stderr: io.Discard,
	})

	if exit != 70 {
		t.Fatalf("Execute() exit = %d, want 70", exit)
	}
	if componentStarted {
		t.Fatal("component ownership transferred before task validation")
	}
}

func TestOneShotTaskFailureUsesExit1AndStillCleansUp(t *testing.T) {
	t.Parallel()

	for _, panicTask := range []bool{false, true} {
		stopped := false
		laterRan := false
		command := service.CommandFor(service.CommandSpec[struct{}]{
			Name: "migrate",
			Kind: service.CommandKindOneShot,
			Load: func(context.Context, service.Invocation) (struct{}, error) {
				return struct{}{}, nil
			},
			Build: func(
				context.Context,
				service.BuildContext,
				struct{},
			) (service.Plan, error) {
				return service.Plan{
					Components: []service.Component{{
						Name:  "database",
						Start: func(context.Context) error { return nil },
						Stop: func(context.Context) error {
							stopped = true

							return nil
						},
					}},
					Tasks: []service.Task{
						{
							Name: "migrate",
							Run: func(context.Context) error {
								if panicTask {
									panic("secret panic")
								}

								return errors.New("migration failed with secret")
							},
						},
						{
							Name: "later",
							Run: func(context.Context) error {
								laterRan = true

								return nil
							},
						},
					},
				}, nil
			},
		})
		var stderr bytes.Buffer
		exit := executeTest(t, context.Background(), service.Definition{
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Migrate: command},
		}, service.Invocation{
			Args:   []string{"migrate"},
			Stdout: io.Discard,
			Stderr: &stderr,
		})
		if exit != 1 {
			t.Fatalf("panic=%v exit = %d, stderr = %q", panicTask, exit, stderr.String())
		}
		if !stopped {
			t.Fatalf("panic=%v component was not stopped", panicTask)
		}
		if laterRan {
			t.Fatalf("panic=%v task after failure ran", panicTask)
		}
		if strings.Contains(stderr.String(), "secret") {
			t.Fatalf("panic=%v disclosed failure: %q", panicTask, stderr.String())
		}
	}
}

func TestExecuteRejectsInvalidPlansBeforeListenerOwnership(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	task := service.Task{
		Name: "task",
		Run:  func(context.Context) error { return nil },
	}
	check := service.ReadinessCheck{
		Name: "database",
		Run:  func(context.Context) error { return nil },
	}
	tests := []struct {
		name       string
		kind       service.CommandKind
		plan       service.Plan
		management service.Management
	}{
		{
			name: "one-shot business HTTP",
			kind: service.CommandKindOneShot,
			plan: service.Plan{HTTP: &service.HTTP{
				Address: "127.0.0.1:0", Handler: http.NotFoundHandler(),
			}},
		},
		{
			name: "duplicate tasks",
			kind: service.CommandKindOneShot,
			plan: service.Plan{Tasks: []service.Task{task, task}},
		},
		{
			name: "duplicate components",
			kind: service.CommandKindOneShot,
			plan: service.Plan{Components: []service.Component{
				{Name: "database"},
				{Name: "database"},
			}},
		},
		{
			name: "invalid readiness",
			kind: service.CommandKindOneShot,
			plan: service.Plan{
				Management: true,
				Readiness:  []service.ReadinessCheck{{Name: "database"}},
			},
		},
		{
			name: "duplicate readiness",
			kind: service.CommandKindOneShot,
			plan: service.Plan{
				Management: true,
				Readiness:  []service.ReadinessCheck{check, check},
			},
		},
		{
			name: "management address and listener",
			kind: service.CommandKindLongRunning,
			management: service.Management{
				Address: "127.0.0.1:0", Listener: listener,
			},
		},
		{
			name: "business missing address and listener",
			kind: service.CommandKindLongRunning,
			plan: service.Plan{HTTP: &service.HTTP{
				Handler: http.NotFoundHandler(),
			}},
		},
		{
			name: "business address and listener",
			kind: service.CommandKindLongRunning,
			plan: service.Plan{HTTP: &service.HTTP{
				Address: "127.0.0.1:0", Listener: listener,
				Handler: http.NotFoundHandler(),
			}},
		},
		{
			name: "business nil handler",
			kind: service.CommandKindLongRunning,
			plan: service.Plan{HTTP: &service.HTTP{
				Address: "127.0.0.1:0",
			}},
		},
		{
			name: "listener collision",
			kind: service.CommandKindLongRunning,
			plan: service.Plan{HTTP: &service.HTTP{
				Listener: listener, Handler: http.NotFoundHandler(),
			}},
			management: service.Management{Listener: listener},
		},
		{
			name: "address collision",
			kind: service.CommandKindLongRunning,
			plan: service.Plan{HTTP: &service.HTTP{
				Address: "127.0.0.1:8081", Handler: http.NotFoundHandler(),
			}},
		},
	}
	for _, test := range tests {
		commandName := "migrate"
		if test.kind == service.CommandKindLongRunning {
			commandName = "worker"
		}
		plan := test.plan
		command := service.CommandFor(service.CommandSpec[struct{}]{
			Name: commandName,
			Kind: test.kind,
			Load: func(context.Context, service.Invocation) (struct{}, error) {
				return struct{}{}, nil
			},
			Build: func(
				context.Context,
				service.BuildContext,
				struct{},
			) (service.Plan, error) {
				return plan, nil
			},
		})
		commands := service.Commands{Migrate: command}
		if test.kind == service.CommandKindLongRunning {
			commands = service.Commands{Worker: command}
		}
		exit := executeTest(t, context.Background(), service.Definition{
			Identity:   service.Identity{Name: "postal"},
			Commands:   commands,
			Management: test.management,
		}, service.Invocation{
			Args:   []string{commandName},
			Stdout: io.Discard,
			Stderr: io.Discard,
		})
		if exit != 70 {
			t.Fatalf("%s exit = %d, want 70", test.name, exit)
		}
	}
}

func TestExecuteRunsLongRunningTasksUntilSignal(t *testing.T) {
	t.Parallel()

	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("management net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = management.Close() })
	var eventsMu sync.Mutex
	var events []string
	record := func(event string) {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		events = append(events, event)
	}
	started := make(chan struct{})
	worker := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "worker",
		Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{
				Components: []service.Component{{
					Name: "queue",
					CloseAdmission: func() error {
						record("close queue admission")

						return nil
					},
					Start: func(context.Context) error {
						record("start queue")

						return nil
					},
					Stop: func(context.Context) error {
						record("stop queue")

						return nil
					},
				}},
				Tasks: []service.Task{{
					Name: "worker",
					Run: func(ctx context.Context) error {
						record("start worker")
						close(started)
						<-ctx.Done()
						record("stop worker")

						return context.Cause(ctx)
					},
				}},
			}, nil
		},
	})
	signals := make(chan os.Signal, 1)
	var stderr bytes.Buffer
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(context.Background(), service.Definition{
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Worker: worker},
			Management: service.Management{
				Listener: management,
			},
		}, service.Invocation{
			Args:    []string{"worker"},
			Signals: signals,
			Stdout:  io.Discard,
			Stderr:  &stderr,
		})
	}()
	select {
	case <-started:
	case exit := <-result:
		t.Fatalf(
			"Execute() exited before the task started with %d, stderr = %q",
			exit,
			stderr.String(),
		)
	}
	signals <- os.Interrupt

	if exit := receiveTestValue(t, result); exit != 130 {
		t.Fatalf("Execute() exit = %d, stderr = %q; want 130", exit, stderr.String())
	}
	eventsMu.Lock()
	got := append([]string(nil), events...)
	eventsMu.Unlock()
	want := []string{
		"start queue",
		"start worker",
		"close queue admission",
		"stop worker",
		"stop queue",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestExecuteUsesSelectedPlanManagementConfiguration(t *testing.T) {
	t.Parallel()

	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("management net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = management.Close() })
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "serve",
		Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{
				ManagementConfig: &service.Management{Listener: management},
				Tasks: []service.Task{{
					Name: "serve",
					Run:  func(context.Context) error { return nil },
				}},
			}, nil
		},
	})
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(context.Background(), service.Definition{
			Identity:   service.Identity{Name: "forex"},
			Commands:   service.Commands{Serve: command},
			Management: service.Management{Address: "missing-port"},
		}, service.Invocation{
			Args:   []string{"serve"},
			Stdout: io.Discard,
			Stderr: io.Discard,
		})
	}()
	exit := receiveTestValue(t, result)
	if exit != 70 {
		t.Fatalf("Execute() exit = %d, want task failure exit 70", exit)
	}
}

func TestExecuteCancelsOneShotTaskOnSignal(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	cause := make(chan error, 1)
	migrate := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{Tasks: []service.Task{{
				Name: "migration",
				Run: func(ctx context.Context) error {
					close(started)
					<-ctx.Done()
					cause <- context.Cause(ctx)

					return context.Cause(ctx)
				},
			}}}, nil
		},
	})
	signals := make(chan os.Signal, 1)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stderr bytes.Buffer
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(parent, service.Definition{
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Migrate: migrate},
		}, service.Invocation{
			Args:    []string{"migrate"},
			Signals: signals,
			Stdout:  io.Discard,
			Stderr:  &stderr,
		})
	}()
	receiveTestValue(t, started)
	signals <- os.Interrupt

	select {
	case exit := <-result:
		if exit != 130 {
			t.Fatalf("Execute() exit = %d, stderr = %q; want 130", exit, stderr.String())
		}
	case <-time.After(100 * time.Millisecond):
		cancel()
		exit := receiveTestValue(t, result)
		t.Fatalf(
			"Execute() ignored signal and exited with %d after parent cancellation",
			exit,
		)
	}
	var signalError *service.SignalError
	if taskCause := receiveTestValue(t, cause); !errors.As(taskCause, &signalError) {
		t.Fatalf("task cancellation cause = %v, want SignalError", taskCause)
	}
}

func TestExecuteCancelsOneShotStartupOnSignal(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	migrate := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{Components: []service.Component{{
				Name: "database",
				Start: func(ctx context.Context) error {
					close(started)
					<-ctx.Done()

					return ctx.Err()
				},
			}}}, nil
		},
	})
	signals := make(chan os.Signal, 1)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stderr bytes.Buffer
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(parent, service.Definition{
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Migrate: migrate},
		}, service.Invocation{
			Args:    []string{"migrate"},
			Signals: signals,
			Stdout:  io.Discard,
			Stderr:  &stderr,
		})
	}()
	receiveTestValue(t, started)
	signals <- os.Interrupt

	select {
	case exit := <-result:
		if exit != 130 {
			t.Fatalf("Execute() exit = %d, stderr = %q; want 130", exit, stderr.String())
		}
	case <-time.After(100 * time.Millisecond):
		cancel()
		exit := receiveTestValue(t, result)
		t.Fatalf(
			"Execute() ignored startup signal and exited with %d after parent cancellation",
			exit,
		)
	}
}

func TestExecuteClassifiesClosedOneShotSignalChannel(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	signals := make(chan os.Signal)
	migrate := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{Tasks: []service.Task{{
				Name: "migration",
				Run: func(ctx context.Context) error {
					close(started)
					<-ctx.Done()

					return context.Cause(ctx)
				},
			}}}, nil
		},
	})
	var stderr bytes.Buffer
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(context.Background(), service.Definition{
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Migrate: migrate},
		}, service.Invocation{
			Args:    []string{"migrate"},
			Signals: signals,
			Stdout:  io.Discard,
			Stderr:  &stderr,
		})
	}()
	receiveTestValue(t, started)
	close(signals)

	if exit := receiveTestValue(t, result); exit != 1 {
		t.Fatalf("Execute() exit = %d, stderr = %q; want 1", exit, stderr.String())
	}
}

func TestOneShotCleanupFailureOverridesSignalExit(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	signals := make(chan os.Signal, 1)
	migrate := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{
				Components: []service.Component{{
					Name:  "database",
					Start: func(context.Context) error { return nil },
					Stop: func(context.Context) error {
						return errors.New("cleanup failed with secret")
					},
				}},
				Tasks: []service.Task{{
					Name: "migration",
					Run: func(ctx context.Context) error {
						close(started)
						<-ctx.Done()

						return context.Cause(ctx)
					},
				}},
			}, nil
		},
	})
	var stderr bytes.Buffer
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(context.Background(), service.Definition{
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Migrate: migrate},
		}, service.Invocation{
			Args:    []string{"migrate"},
			Signals: signals,
			Stdout:  io.Discard,
			Stderr:  &stderr,
		})
	}()
	receiveTestValue(t, started)
	signals <- os.Interrupt

	if exit := receiveTestValue(t, result); exit != 70 {
		t.Fatalf("Execute() exit = %d, stderr = %q; want 70", exit, stderr.String())
	}
	if strings.Contains(stderr.String(), "secret") {
		t.Fatalf("stderr disclosed cleanup error: %q", stderr.String())
	}
}

func TestExecuteGracefullyCancelsOneShotTaskWithParent(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	stopped := false
	migrate := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{
				Components: []service.Component{{
					Name:  "database",
					Start: func(context.Context) error { return nil },
					Stop: func(context.Context) error {
						stopped = true

						return nil
					},
				}},
				Tasks: []service.Task{{
					Name: "migration",
					Run: func(ctx context.Context) error {
						close(started)
						<-ctx.Done()

						return context.Cause(ctx)
					},
				}},
			}, nil
		},
	})
	parent, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal)
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(parent, service.Definition{
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Migrate: migrate},
		}, service.Invocation{
			Args:    []string{"migrate"},
			Signals: signals,
			Stdout:  io.Discard,
			Stderr:  io.Discard,
		})
	}()
	receiveTestValue(t, started)
	cancel()

	if exit := receiveTestValue(t, result); exit != 0 {
		t.Fatalf("Execute() exit = %d, want 0", exit)
	}
	if !stopped {
		t.Fatal("component was not stopped")
	}
}

func TestOneShotTaskFailureOverridesParentCancellation(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	migrate := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{Tasks: []service.Task{{
				Name: "migration",
				Run: func(ctx context.Context) error {
					close(started)
					<-ctx.Done()

					return errors.New("migration failed")
				},
			}}}, nil
		},
	})
	parent, cancel := context.WithCancel(context.Background())
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(parent, service.Definition{
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Migrate: migrate},
		}, service.Invocation{
			Args:   []string{"migrate"},
			Stdout: io.Discard,
			Stderr: io.Discard,
		})
	}()
	receiveTestValue(t, started)
	cancel()

	if exit := receiveTestValue(t, result); exit != 1 {
		t.Fatalf("Execute() exit = %d, want 1", exit)
	}
}

func TestSecondOneShotSignalCancelsCleanup(t *testing.T) {
	t.Parallel()

	taskStarted := make(chan struct{})
	cleanupStarted := make(chan struct{})
	cleanupCanceled := make(chan struct{})
	signals := make(chan os.Signal, 2)
	migrate := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{
				Components: []service.Component{{
					Name:  "database",
					Start: func(context.Context) error { return nil },
					Stop: func(ctx context.Context) error {
						close(cleanupStarted)
						<-ctx.Done()
						close(cleanupCanceled)

						return context.Cause(ctx)
					},
				}},
				Tasks: []service.Task{{
					Name: "migration",
					Run: func(ctx context.Context) error {
						close(taskStarted)
						<-ctx.Done()

						return context.Cause(ctx)
					},
				}},
			}, nil
		},
	})
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(context.Background(), service.Definition{
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Migrate: migrate},
		}, service.Invocation{
			Args:    []string{"migrate"},
			Signals: signals,
			Stdout:  io.Discard,
			Stderr:  io.Discard,
		})
	}()
	receiveTestValue(t, taskStarted)
	signals <- os.Interrupt
	receiveTestValue(t, cleanupStarted)
	signals <- os.Interrupt

	if exit := receiveTestValue(t, result); exit != 70 {
		t.Fatalf("Execute() exit = %d, want 70", exit)
	}
	receiveTestValue(t, cleanupCanceled)
}

func TestSecondLongRunningSignalCancelsCleanup(t *testing.T) {
	t.Parallel()

	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("management net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = management.Close() })

	taskStarted := make(chan struct{})
	cleanupStarted := make(chan struct{})
	cleanupCanceled := make(chan struct{})
	signals := make(chan os.Signal, 2)
	worker := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "worker",
		Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{
				Components: []service.Component{{
					Name:  "database",
					Start: func(context.Context) error { return nil },
					Stop: func(ctx context.Context) error {
						close(cleanupStarted)
						<-ctx.Done()
						close(cleanupCanceled)

						return context.Cause(ctx)
					},
				}},
				Tasks: []service.Task{{
					Name: "worker",
					Run: func(ctx context.Context) error {
						close(taskStarted)
						<-ctx.Done()

						return context.Cause(ctx)
					},
				}},
			}, nil
		},
	})
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(context.Background(), service.Definition{
			Identity:   service.Identity{Name: "postal"},
			Commands:   service.Commands{Worker: worker},
			Management: service.Management{Listener: management},
		}, service.Invocation{
			Args:    []string{"worker"},
			Signals: signals,
			Stdout:  io.Discard,
			Stderr:  io.Discard,
		})
	}()
	receiveTestValue(t, taskStarted)
	signals <- os.Interrupt
	receiveTestValue(t, cleanupStarted)
	signals <- os.Interrupt

	if exit := receiveTestValue(t, result); exit != 70 {
		t.Fatalf("Execute() exit = %d, want 70", exit)
	}
	receiveTestValue(t, cleanupCanceled)
}

func TestClosingOneShotSignalsAfterCancellationDoesNotAbortCleanup(t *testing.T) {
	t.Parallel()

	taskStarted := make(chan struct{})
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	signals := make(chan os.Signal, 1)
	migrate := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{
				Components: []service.Component{{
					Name:  "database",
					Start: func(context.Context) error { return nil },
					Stop: func(context.Context) error {
						close(cleanupStarted)
						<-releaseCleanup

						return nil
					},
				}},
				Tasks: []service.Task{{
					Name: "migration",
					Run: func(ctx context.Context) error {
						close(taskStarted)
						<-ctx.Done()

						return context.Cause(ctx)
					},
				}},
			}, nil
		},
	})
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(context.Background(), service.Definition{
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Migrate: migrate},
		}, service.Invocation{
			Args:    []string{"migrate"},
			Signals: signals,
			Stdout:  io.Discard,
			Stderr:  io.Discard,
		})
	}()
	receiveTestValue(t, taskStarted)
	signals <- os.Interrupt
	receiveTestValue(t, cleanupStarted)
	close(signals)
	close(releaseCleanup)

	if exit := receiveTestValue(t, result); exit != 130 {
		t.Fatalf("Execute() exit = %d, want 130", exit)
	}
}

func TestOneShotCleanupFailureOverridesParentCancellation(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	migrate := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{
				Components: []service.Component{{
					Name:  "database",
					Start: func(context.Context) error { return nil },
					Stop: func(context.Context) error {
						return errors.New("cleanup failed")
					},
				}},
				Tasks: []service.Task{{
					Name: "migration",
					Run: func(ctx context.Context) error {
						close(started)
						<-ctx.Done()

						return context.Cause(ctx)
					},
				}},
			}, nil
		},
	})
	parent, cancel := context.WithCancel(context.Background())
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(parent, service.Definition{
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Migrate: migrate},
		}, service.Invocation{
			Args:   []string{"migrate"},
			Stdout: io.Discard,
			Stderr: io.Discard,
		})
	}()
	receiveTestValue(t, started)
	cancel()

	if exit := receiveTestValue(t, result); exit != 70 {
		t.Fatalf("Execute() exit = %d, want 70", exit)
	}
}

func TestLongRunningTaskFailureUsesExit70(t *testing.T) {
	t.Parallel()

	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("management net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = management.Close() })
	worker := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "worker",
		Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{Tasks: []service.Task{{
				Name: "worker",
				Run: func(context.Context) error {
					return errors.New("queue runtime failed with token=secret")
				},
			}}}, nil
		},
	})
	var stderr bytes.Buffer
	exit := executeTest(t, context.Background(), service.Definition{
		Identity:   service.Identity{Name: "postal"},
		Commands:   service.Commands{Worker: worker},
		Management: service.Management{Listener: management},
	}, service.Invocation{
		Args:   []string{"worker"},
		Stdout: io.Discard,
		Stderr: &stderr,
	})

	if exit != 70 {
		t.Fatalf("Execute() exit = %d, stderr = %q; want 70", exit, stderr.String())
	}
	if strings.Contains(stderr.String(), "secret") {
		t.Fatalf("stderr disclosed runtime failure: %q", stderr.String())
	}
}

func TestLongRunningTaskCannotExitSuccessfully(t *testing.T) {
	t.Parallel()

	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("management net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = management.Close() })
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "worker",
		Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{Tasks: []service.Task{{
				Name: "worker",
				Run:  func(context.Context) error { return nil },
			}}}, nil
		},
	})
	exit := executeTest(t, context.Background(), service.Definition{
		Identity:   service.Identity{Name: "postal"},
		Commands:   service.Commands{Worker: command},
		Management: service.Management{Listener: management},
	}, service.Invocation{
		Args:   []string{"worker"},
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if exit != 70 {
		t.Fatalf("Execute() exit = %d, want 70", exit)
	}
}

func TestLongRunningPlanRejectsExcessTaskCapacity(t *testing.T) {
	t.Parallel()

	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("management net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = management.Close() })
	tasks := make([]service.Task, 65)
	for index := range tasks {
		tasks[index] = service.Task{
			Name: fmt.Sprintf("worker-%d", index),
			Run: func(ctx context.Context) error {
				<-ctx.Done()

				return context.Cause(ctx)
			},
		}
	}
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "worker",
		Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{Tasks: tasks}, nil
		},
	})
	exit := executeTest(t, context.Background(), service.Definition{
		Identity:   service.Identity{Name: "postal"},
		Commands:   service.Commands{Worker: command},
		Management: service.Management{Listener: management},
	}, service.Invocation{
		Args:   []string{"worker"},
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if exit != 70 {
		t.Fatalf("Execute() exit = %d, want 70", exit)
	}
}

func TestLongRunningBusinessHTTPExtendsSupervisedTaskCapacity(t *testing.T) {
	t.Parallel()

	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("management net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = management.Close() })
	business, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("business net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = business.Close() })

	started := make(chan struct{}, 64)
	tasks := make([]service.Task, 64)
	for index := range tasks {
		tasks[index] = service.Task{
			Name: fmt.Sprintf("worker-%d", index),
			Run: func(ctx context.Context) error {
				started <- struct{}{}
				<-ctx.Done()

				return context.Cause(ctx)
			},
		}
	}
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "worker",
		Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{
				HTTP: &service.HTTP{
					Listener: business,
					Handler:  http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
				},
				Tasks: tasks,
			}, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(ctx, service.Definition{
			Identity:   service.Identity{Name: "postal"},
			Commands:   service.Commands{Worker: command},
			Management: service.Management{Listener: management},
		}, service.Invocation{
			Args:   []string{"worker"},
			Stdout: io.Discard,
			Stderr: io.Discard,
		})
	}()
	for range tasks {
		receiveTestValue(t, started)
	}
	cancel()
	if exit := receiveTestValue(t, result); exit != 0 {
		t.Fatalf("Execute() exit = %d, want 0", exit)
	}
}

func TestLongRunningParentCancellationIsSuccessful(t *testing.T) {
	t.Parallel()

	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("management net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = management.Close() })
	started := make(chan struct{})
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "worker",
		Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{
				Readiness: []service.ReadinessCheck{{
					Name: "queue",
					Run:  func(context.Context) error { return nil },
				}},
				Tasks: []service.Task{{
					Name: "worker",
					Run: func(ctx context.Context) error {
						close(started)
						<-ctx.Done()

						return context.Cause(ctx)
					},
				}},
			}, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan int, 1)
	var stderr bytes.Buffer
	go func() {
		result <- service.Execute(ctx, service.Definition{
			Identity:   service.Identity{Name: "postal"},
			Commands:   service.Commands{Worker: command},
			Management: service.Management{Listener: management},
		}, service.Invocation{
			Args:   []string{"worker"},
			Stdout: io.Discard,
			Stderr: &stderr,
		})
	}()
	receiveTestValue(t, started)
	cancel()
	if exit := receiveTestValue(t, result); exit != 0 {
		t.Fatalf("Execute() exit = %d, stderr = %q; want 0", exit, stderr.String())
	}
}

func TestLongRunningCommandServesCanonicalManagementProbes(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	started := make(chan struct{})
	worker := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "worker",
		Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{Tasks: []service.Task{{
				Name: "worker",
				Run: func(ctx context.Context) error {
					close(started)
					<-ctx.Done()

					return context.Cause(ctx)
				},
			}}}, nil
		},
	})
	signals := make(chan os.Signal, 1)
	var stderr bytes.Buffer
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(context.Background(), service.Definition{
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Worker: worker},
			Management: service.Management{
				Listener:                 listener,
				RejectInvalidCorrelation: true,
			},
		}, service.Invocation{
			Args:    []string{"worker"},
			Signals: signals,
			Stdout:  io.Discard,
			Stderr:  &stderr,
		})
	}()
	t.Cleanup(func() {
		select {
		case signals <- os.Interrupt:
		default:
		}
	})
	select {
	case <-started:
	case exit := <-result:
		t.Fatalf("Execute() exited before startup with %d: %s", exit, stderr.String())
	}

	client := &http.Client{Timeout: time.Second}
	for _, path := range []string{"/livez", "/startupz", "/readyz"} {
		response, requestErr := client.Get("http://" + listener.Addr().String() + path)
		if requestErr != nil {
			t.Fatalf("GET %s error = %v", path, requestErr)
		}
		if response.StatusCode != http.StatusOK {
			_ = response.Body.Close()
			t.Fatalf("GET %s status = %d, want 200", path, response.StatusCode)
		}
		correlationID := response.Header.Get("X-Correlation-ID")
		requestID := response.Header.Get("X-Request-ID")
		if correlationID == "" || requestID == "" || correlationID == requestID {
			_ = response.Body.Close()
			t.Fatalf(
				"GET %s correlation = %q, request = %q",
				path,
				correlationID,
				requestID,
			)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatalf("GET %s close error = %v", path, err)
		}
	}
	invalidRequest, err := http.NewRequest(
		http.MethodGet,
		"http://"+listener.Addr().String()+"/livez",
		nil,
	)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	invalidRequest.Header.Set("X-Correlation-ID", "invalid value")
	invalidResponse, err := client.Do(invalidRequest)
	if err != nil {
		t.Fatalf("invalid correlation request error = %v", err)
	}
	if invalidResponse.StatusCode != http.StatusBadRequest {
		_ = invalidResponse.Body.Close()
		t.Fatalf("invalid correlation status = %d, want 400", invalidResponse.StatusCode)
	}
	if invalidResponse.Header.Get("X-Correlation-ID") == "" ||
		invalidResponse.Header.Get("X-Request-ID") == "" {
		_ = invalidResponse.Body.Close()
		t.Fatalf("invalid correlation response lacks identity: %q", invalidResponse.Header)
	}
	_ = invalidResponse.Body.Close()

	signals <- os.Interrupt
	if exit := receiveTestValue(t, result); exit != 130 {
		t.Fatalf("Execute() exit = %d, stderr = %q; want 130", exit, stderr.String())
	}
}

func TestServeCommandOwnsBusinessHTTPWithCorrelationAndTraceOrder(t *testing.T) {
	t.Parallel()

	business, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("business net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = business.Close() })
	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("management net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = management.Close() })

	traceCalled := make(chan struct{}, 1)
	trace := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if _, ok := correlation.FromContext(request.Context()); !ok {
				t.Fatal("trace middleware ran before correlation")
			}
			traceCalled <- struct{}{}
			next.ServeHTTP(writer, request)
		})
	}
	serve := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "serve",
		Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{HTTP: &service.HTTP{
				Listener:                 business,
				RejectInvalidCorrelation: true,
				Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					assertCorrelationContext(t, request.Context())
					_, _ = io.WriteString(writer, "ok")
				}),
			}}, nil
		},
	})
	signals := make(chan os.Signal, 1)
	var stderr bytes.Buffer
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(context.Background(), service.Definition{
			Identity:         service.Identity{Name: "postal"},
			Commands:         service.Commands{Serve: serve},
			TracePropagation: trace,
			Management:       service.Management{Listener: management},
		}, service.Invocation{
			Args:    []string{"serve"},
			Signals: signals,
			Stdout:  io.Discard,
			Stderr:  &stderr,
		})
	}()
	t.Cleanup(func() {
		select {
		case signals <- os.Interrupt:
		default:
		}
	})

	request, err := http.NewRequest(
		http.MethodGet,
		"http://"+business.Addr().String()+"/",
		nil,
	)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	request.Header.Set("X-Correlation-ID", "untrusted-caller")
	client := &http.Client{Timeout: time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("business request error = %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("response body Close() error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("business status = %d, want 200", response.StatusCode)
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	correlationID := response.Header.Get("X-Correlation-ID")
	requestID := response.Header.Get("X-Request-ID")
	if correlationID == "" || correlationID == "untrusted-caller" ||
		requestID == "" || requestID == correlationID {
		t.Fatalf("correlation = %q, request = %q", correlationID, requestID)
	}
	select {
	case <-traceCalled:
	default:
		t.Fatal("trace middleware was not called")
	}

	signals <- os.Interrupt
	if exit := receiveTestValue(t, result); exit != 130 {
		t.Fatalf("Execute() exit = %d, stderr = %q; want 130", exit, stderr.String())
	}
}

func TestBusinessHTTPDrainsBeforeUnrelatedSupervisedWorkJoins(t *testing.T) {
	t.Parallel()

	rawBusiness, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("business net.Listen() error = %v", err)
	}
	business := &closeObservedListener{
		Listener: rawBusiness,
		closed:   make(chan struct{}),
	}
	t.Cleanup(func() { _ = business.Close() })
	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("management net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = management.Close() })

	workerStarted := make(chan struct{})
	workerCanceled := make(chan struct{})
	releaseWorker := make(chan struct{})
	serve := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "serve",
		Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{
				HTTP: &service.HTTP{
					Listener: business,
					Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
						writer.WriteHeader(http.StatusNoContent)
					}),
				},
				Tasks: []service.Task{{
					Name: "worker",
					Run: func(ctx context.Context) error {
						close(workerStarted)
						<-ctx.Done()
						close(workerCanceled)
						<-releaseWorker

						return context.Cause(ctx)
					},
				}},
			}, nil
		},
	})
	signals := make(chan os.Signal, 1)
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(context.Background(), service.Definition{
			Identity: service.Identity{Name: "postal"},
			Commands: service.Commands{Serve: serve},
			Management: service.Management{
				Listener: management,
			},
		}, service.Invocation{
			Args:    []string{"serve"},
			Signals: signals,
			Stdout:  io.Discard,
			Stderr:  io.Discard,
		})
	}()
	receiveTestValue(t, workerStarted)
	signals <- os.Interrupt
	receiveTestValue(t, workerCanceled)

	select {
	case <-business.closed:
	case <-time.After(100 * time.Millisecond):
		close(releaseWorker)
		receiveTestValue(t, result)
		t.Fatal("business listener remained open while supervised work drained")
	}
	close(releaseWorker)
	if exit := receiveTestValue(t, result); exit != 130 {
		t.Fatalf("Execute() exit = %d, want 130", exit)
	}
}

func TestOwnedHTTPConstructionSuccessAndFailurePaths(t *testing.T) {
	t.Parallel()

	nilMiddleware := func(http.Handler) http.Handler { return nil }
	tests := []struct {
		name       string
		management service.Management
		http       *service.HTTP
		trace      func(http.Handler) http.Handler
		exit       int
	}{
		{
			name: "management bind failure",
			management: service.Management{
				Address: "missing-port",
			},
			exit: 75,
		},
		{
			name: "management middleware failure after owned bind",
			management: service.Management{
				Address: "127.0.0.1:0",
			},
			trace: nilMiddleware,
			exit:  75,
		},
		{
			name: "business bind failure",
			management: service.Management{
				Address: "localhost:0",
			},
			http: &service.HTTP{
				Address: "missing-port",
				Handler: http.NotFoundHandler(),
			},
			exit: 75,
		},
		{
			name: "business option failure after owned bind",
			management: service.Management{
				Address: "localhost:0",
			},
			http: &service.HTTP{
				Address: "127.0.0.1:0",
				Handler: http.NotFoundHandler(),
				Options: []serverhttp.Option{nil},
			},
			exit: 75,
		},
		{
			name: "owned management and business listeners",
			management: service.Management{
				Address: "localhost:0",
			},
			http: &service.HTTP{
				Address: "127.0.0.1:0",
				Handler: http.NotFoundHandler(),
			},
			exit: 70,
		},
	}
	for _, test := range tests {
		httpPlan := test.http
		command := service.CommandFor(service.CommandSpec[struct{}]{
			Name: "serve",
			Kind: service.CommandKindLongRunning,
			Load: func(context.Context, service.Invocation) (struct{}, error) {
				return struct{}{}, nil
			},
			Build: func(
				context.Context,
				service.BuildContext,
				struct{},
			) (service.Plan, error) {
				return service.Plan{
					HTTP: httpPlan,
					Tasks: []service.Task{{
						Name: "serve",
						Run:  func(context.Context) error { return nil },
					}},
				}, nil
			},
		})
		exit := executeTest(t, context.Background(), service.Definition{
			Identity:         service.Identity{Name: "postal"},
			Commands:         service.Commands{Serve: command},
			TracePropagation: test.trace,
			Management:       test.management,
		}, service.Invocation{
			Args:   []string{"serve"},
			Stdout: io.Discard,
			Stderr: io.Discard,
		})
		if exit != test.exit {
			t.Fatalf("%s exit = %d, want %d", test.name, exit, test.exit)
		}
	}
}

func TestProvidedListenersCloseAfterValidatedConstructionFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		failBusiness   bool
		trace          serverhttp.Middleware
		businessOption serverhttp.Option
	}{
		{
			name: "management middleware construction",
			trace: func(http.Handler) http.Handler {
				return nil
			},
		},
		{
			name: "management middleware panic",
			trace: func(http.Handler) http.Handler {
				panic("secret")
			},
		},
		{
			name:           "business option construction",
			failBusiness:   true,
			businessOption: nil,
		},
		{
			name:         "business middleware panic",
			failBusiness: true,
			businessOption: serverhttp.WithMiddleware(func(
				http.Handler,
			) http.Handler {
				panic("secret")
			}),
		},
	}
	for _, test := range tests {
		management, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("%s management net.Listen() error = %v", test.name, err)
		}
		var business net.Listener
		if test.failBusiness {
			business, err = net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				_ = management.Close()
				t.Fatalf("%s business net.Listen() error = %v", test.name, err)
			}
		}
		command := service.CommandFor(service.CommandSpec[struct{}]{
			Name: "serve",
			Kind: service.CommandKindLongRunning,
			Load: func(context.Context, service.Invocation) (struct{}, error) {
				return struct{}{}, nil
			},
			Build: func(
				context.Context,
				service.BuildContext,
				struct{},
			) (service.Plan, error) {
				plan := service.Plan{}
				if business != nil {
					plan.HTTP = &service.HTTP{
						Listener: business,
						Handler:  http.NotFoundHandler(),
						Options:  []serverhttp.Option{test.businessOption},
					}
				}

				return plan, nil
			},
		})
		exit := executeTest(t, context.Background(), service.Definition{
			Identity:         service.Identity{Name: "postal"},
			Commands:         service.Commands{Serve: command},
			Management:       service.Management{Listener: management},
			TracePropagation: test.trace,
		}, service.Invocation{
			Args:   []string{"serve"},
			Stdout: io.Discard,
			Stderr: io.Discard,
		})
		if exit != 75 {
			t.Fatalf("%s exit = %d, want 75", test.name, exit)
		}
		if err := management.Close(); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("%s management Close() error = %v, want net.ErrClosed", test.name, err)
		}
		if business != nil {
			if err := business.Close(); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("%s business Close() error = %v, want net.ErrClosed", test.name, err)
			}
		}
	}
}

func TestOwnedHTTPRuntimeFailureTerminatesTheCommand(t *testing.T) {
	t.Parallel()

	for _, failBusiness := range []bool{false, true} {
		management, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("management net.Listen() error = %v", err)
		}
		business, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			_ = management.Close()
			t.Fatalf("business net.Listen() error = %v", err)
		}
		started := make(chan struct{})
		command := service.CommandFor(service.CommandSpec[struct{}]{
			Name: "serve",
			Kind: service.CommandKindLongRunning,
			Load: func(context.Context, service.Invocation) (struct{}, error) {
				return struct{}{}, nil
			},
			Build: func(
				context.Context,
				service.BuildContext,
				struct{},
			) (service.Plan, error) {
				return service.Plan{
					HTTP: &service.HTTP{
						Listener: business,
						Handler:  http.NotFoundHandler(),
					},
					Tasks: []service.Task{{
						Name: "serve",
						Run: func(ctx context.Context) error {
							close(started)
							<-ctx.Done()

							return context.Cause(ctx)
						},
					}},
				}, nil
			},
		})
		result := make(chan int, 1)
		go func() {
			result <- service.Execute(context.Background(), service.Definition{
				Identity:   service.Identity{Name: "postal"},
				Commands:   service.Commands{Serve: command},
				Management: service.Management{Listener: management},
			}, service.Invocation{
				Args:   []string{"serve"},
				Stdout: io.Discard,
				Stderr: io.Discard,
			})
		}()
		receiveTestValue(t, started)
		if failBusiness {
			_ = business.Close()
		} else {
			_ = management.Close()
		}
		if exit := receiveTestValue(t, result); exit != 70 {
			t.Fatalf("business=%v exit = %d, want 70", failBusiness, exit)
		}
		_ = business.Close()
		_ = management.Close()
	}
}

func assertCorrelationContext(t *testing.T, ctx context.Context) {
	t.Helper()

	values, ok := correlation.FromContext(ctx)
	if !ok || values.CorrelationID == "" || values.RequestID == "" {
		t.Fatalf("correlation context = %#v, %v", values, ok)
	}
}
