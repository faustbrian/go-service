package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/faustbrian/go-cli/v2"
	"github.com/faustbrian/go-correlation"
	httpcorrelation "github.com/faustbrian/go-correlation/adapters/http"
	"github.com/faustbrian/go-service/v3/healthhttp"
	"github.com/faustbrian/go-service/v3/serverhttp"
)

const (
	exitSuccess       = 0
	exitCommand       = 1
	exitUsage         = 2
	exitSoftware      = 70
	exitTemporary     = 75
	exitConfiguration = 78
)

// Identity describes one deployable service.
type Identity struct {
	// Name is the stable lowercase service name.
	Name string
	// Version is the semantic build version, or empty when unavailable.
	Version string
	// Commit is the hexadecimal source revision, or empty when unavailable.
	Commit string
	// BuildTime is an RFC3339 build timestamp, or empty when unavailable.
	BuildTime string
	// GoVersion is the Go toolchain version, or empty when unavailable.
	GoVersion string
	// Environment identifies the deployment environment without becoming a
	// metric label.
	Environment string
	// Instance identifies the process instance without becoming a metric label.
	Instance string
}

// ProcessIdentity is the service identity for one selected process role.
type ProcessIdentity struct {
	Identity
	// Role is the selected command name.
	Role string
}

// Definition declares process construction. Treat it and referenced values as
// immutable during execution; a struct copy does not clone callbacks, clients,
// handlers, stores or Custom's backing array. Separate executions can share a
// definition only when borrowed resources support that concurrency and each
// plan acquires distinct owned resources.
type Definition struct {
	// Identity identifies the deployable service.
	Identity Identity
	// Commands declares every supported process role.
	Commands Commands
	// Logger is caller owned. Nil keeps logging disabled; its handler must
	// support concurrent calls, return promptly and avoid disclosing attributes.
	Logger *slog.Logger
	// Observer receives bounded lifecycle, task, probe, and maintenance events.
	Observer RuntimeObserver
	// CorrelationDisclosure controls correlation values included in platform
	// logs. The zero value redacts identifiers.
	CorrelationDisclosure correlation.DisclosurePolicy
	// Correlation is caller owned. Nil selects the correlation default.
	Correlation *correlation.Factory
	// TracePropagation optionally extracts caller-owned trace context after
	// correlation.
	TracePropagation serverhttp.Middleware
	// Management configures the platform-owned operational listener.
	Management Management
	// Maintenance optionally enables shared operational maintenance state,
	// built-in down/up/status commands, readiness withdrawal, and HTTP admission.
	Maintenance Maintenance
}

// Commands declares standard and application-specific process roles.
type Commands struct {
	// Serve registers the standard serve role.
	Serve Command
	// Worker registers the standard worker role.
	Worker Command
	// Schedule registers the standard schedule role.
	Schedule Command
	// Migrate registers the standard migrate role.
	Migrate Command
	// Custom contains explicitly named application commands.
	Custom []Command
}

// CommandKind controls long-running and one-shot runtime behavior.
type CommandKind uint8

const (
	// CommandKindLongRunning runs until cancellation or a runtime failure.
	CommandKindLongRunning CommandKind = iota + 1
	// CommandKindOneShot runs finite tasks and exits after cleanup.
	CommandKindOneShot
)

// Command is an immutable command registration produced by CommandFor.
type Command struct {
	registration *commandRegistration
}

type commandRegistration struct {
	name    string
	summary string
	kind    CommandKind
	options []cli.OptionDefinition
	build   func(context.Context, Invocation, BuildContext) (Plan, error)
	invalid string
}

// CommandSpec declares typed configuration loading and plan construction.
type CommandSpec[C any] struct {
	// Name is the lowercase kebab-case command token.
	Name string
	// Summary is the one-line help description.
	Summary string
	// Kind determines whether the command is long-running or one-shot.
	Kind CommandKind
	// Options declares bounded command-specific CLI input. Load reads the
	// resolved values through Invocation.Input and the declared bindings.
	Options []cli.OptionDefinition
	// Load decodes and validates command-specific configuration.
	Load func(context.Context, Invocation) (C, error)
	// Build constructs the owned runtime plan from typed configuration.
	Build func(context.Context, BuildContext, C) (Plan, error)
}

// CommandFor erases only the immutable command registration while preserving
// concrete configuration in application callbacks. It copies the option slice,
// not option internals or callback state. Missing callbacks are recorded and
// rejected at execution. Load and Build run synchronously with a shared
// 30-second construction context; panics are contained, but blocking callbacks
// are not preempted. Build must clean up partial resources before returning an
// error; a successful plan is not owned until its components start.
func CommandFor[C any](spec CommandSpec[C]) Command {
	invalid := ""
	if spec.Load == nil || spec.Build == nil {
		invalid = "requires load and build callbacks"
	}

	return Command{registration: &commandRegistration{
		name:    spec.Name,
		summary: spec.Summary,
		kind:    spec.Kind,
		options: append([]cli.OptionDefinition(nil), spec.Options...),
		invalid: invalid,
		build: func(
			ctx context.Context,
			invocation Invocation,
			build BuildContext,
		) (Plan, error) {
			configuration, err := loadCommandConfiguration(
				spec.Name,
				spec.Load,
				ctx,
				invocation,
			)
			if err != nil {
				return Plan{}, &ConfigurationError{Command: spec.Name, Err: err}
			}
			plan, err := buildCommandPlan(
				spec.Name,
				spec.Build,
				ctx,
				build,
				configuration,
			)
			if err != nil {
				return Plan{}, &ConstructionError{Command: spec.Name, Err: err}
			}

			return plan, nil
		},
	}}
}

func loadCommandConfiguration[C any](
	command string,
	load func(context.Context, Invocation) (C, error),
	ctx context.Context,
	invocation Invocation,
) (configuration C, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = &PanicError{
				Component: command,
				Operation: "load configuration",
				Value:     value,
			}
		}
	}()

	return load(ctx, invocation)
}

func buildCommandPlan[C any](
	command string,
	build func(context.Context, BuildContext, C) (Plan, error),
	ctx context.Context,
	buildContext BuildContext,
	configuration C,
) (plan Plan, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = &PanicError{
				Component: command,
				Operation: "build plan",
				Value:     value,
			}
		}
	}()

	return build(ctx, buildContext, configuration)
}

// Invocation is an in-process command request. Execute copies Args and
// Environment before dispatch, and callbacks must treat those slices as
// read-only. Writers and signal channels remain borrowed; their blocking,
// concurrency and disclosure policies are the caller's responsibility.
type Invocation struct {
	// Args is the tokenized argument list without the process name.
	Args []string
	// Environment is the immutable process-environment snapshot.
	Environment []string
	// Stdout receives help, version, and successful command output.
	Stdout io.Writer
	// Stderr receives one safe terminal diagnostic.
	Stderr io.Writer
	// Signals supplies deterministic cancellation events. Nil follows only the
	// parent context.
	Signals            <-chan os.Signal
	input              cli.Input
	beforeSignalCancel func(func())
}

// Input returns the selected command's canonical parsed CLI input for use with
// the bindings declared in CommandSpec.Options. Values and their provenance are
// invocation-local and remain available after Execute returns. Collection copying
// and custom-value ownership follow cli.Input's contract. A zero Invocation
// returns zero input with omitted values; Execute populates it only after parsing.
func (invocation Invocation) Input() cli.Input { return invocation.input }

// BuildContext contains immutable platform-owned construction values.
type BuildContext struct {
	// Identity identifies the service and selected process role.
	Identity ProcessIdentity
	// Logger is the optional caller-owned logger selected by the definition.
	Logger *slog.Logger
	// Correlation creates identifiers for platform-managed work boundaries.
	Correlation *correlation.Factory
}

// Plan declares resources and work for the selected command. Execution shallow
// copies its slices and pointed-to HTTP/Management values, not callback state,
// handlers or listeners. Validation failure does not clean up caller resources.
// One-shot tasks run sequentially and stop on first failure; long-running tasks
// run concurrently and a nil return before cancellation is treated as failure.
type Plan struct {
	// ShutdownTimeout bounds total post-start shutdown waiting, including task
	// joining and reverse-order component cleanup. Zero uses 30 seconds;
	// negative values are invalid. The allowance starts at shutdown, not startup,
	// and does not replace the separate startup-rollback or HTTP drain limits.
	// Callbacks must cooperate with cancellation; a timeout cannot terminate them.
	ShutdownTimeout time.Duration
	// ComponentStopTimeout opts component cleanup into its own phase budget.
	// Zero keeps one ShutdownTimeout for the whole shutdown; negative values
	// are invalid. When positive, ShutdownTimeout bounds only the work before
	// component stopping: admission closure, cancellation and the join of
	// tasks, business HTTP drain and handler finalization. If that join
	// completes in time, every component Stop shares one fresh deadline of
	// ComponentStopTimeout measured from the first Stop, and the platform's
	// wait extends to it, so the longest wait is the sum of both values. A
	// join that exhausts ShutdownTimeout reports ShutdownTimeoutError without
	// granting the component budget, and a second signal cancels cleanup
	// without granting time. Startup rollback keeps its own limit.
	ComponentStopTimeout time.Duration
	// StartupTimeout bounds component acquisition: every Component.Start,
	// including the platform's management and HTTP owners, shares one
	// deadline measured from the beginning of startup. Zero keeps
	// the 30-second default; negative values are invalid. It ends when
	// startup succeeds and never overlaps ShutdownTimeout or
	// ComponentStopTimeout.
	StartupTimeout time.Duration
	// RollbackTimeout bounds the reverse-order Stop of already started
	// components after a failed or canceled startup, measured from the
	// failure. Zero keeps the 30-second default; negative values are invalid.
	// Rollback happens before any task runs, so ShutdownTimeout and
	// ComponentStopTimeout never apply to it and it does not consume them.
	RollbackTimeout time.Duration
	// Components start in declaration order and stop in reverse order.
	Components []Component
	// Tasks are finite one-shot work or supervised long-running work.
	Tasks []Task
	// HTTP optionally declares one business HTTP listener for a long-running
	// command.
	HTTP *HTTP
	// Readiness contains only dependencies required to accept new work.
	Readiness []ReadinessCheck
	// Management explicitly enables probes for a one-shot command. It is
	// ignored for long-running commands, which always expose probes.
	Management bool
	// ManagementConfig overrides the definition-level management listener for
	// the selected plan. The platform snapshots the pointed-to value.
	ManagementConfig *Management
}

// Task is one named owned unit of application work. Run must cooperate with
// cancellation; the platform contains panics but cannot forcibly stop it.
type Task struct {
	// Name is unique within the declared application task list and must be safe
	// for diagnostics; names are limited to 128 bytes.
	Name string
	// Run executes owned work and must honor context cancellation.
	Run func(context.Context) error
}

// HTTP declares one caller-owned business handler and listener boundary.
type HTTP struct {
	// Address is bound by the platform. Exactly one of Address and Listener is
	// required.
	Address string
	// Listener is borrowed until the HTTP start callback acquires it. Validation
	// failure or an earlier component's failure does not close it; attempted
	// server construction closes it on failure, and success transfers ownership.
	Listener net.Listener
	// Handler owns application routing and protocol behavior.
	Handler http.Handler
	// JoinHandlers retains the business HTTP task until every admitted handler
	// returns, including after forced close or serving failure. Draining rejects
	// new handler admission. False preserves the default Serve-only join.
	// This does not detach request contexts from process cancellation; callers
	// can select that policy through serverhttp.WithConnContext. Application-
	// spawned work and hijacked connections remain caller-owned. Non-cooperative
	// handlers can exhaust Plan.ShutdownTimeout; that reports incomplete cleanup
	// rather than closing dependent components underneath them. The allowance
	// must include HTTP drain, handler finalization and component cleanup.
	JoinHandlers bool
	// Options explicitly customize the serverhttp runtime.
	Options []serverhttp.Option
	// TrustCorrelation authenticates an immediate peer before inbound
	// correlation metadata is preserved.
	TrustCorrelation func(*http.Request) bool
	// RejectInvalidCorrelation returns HTTP 400 instead of replacing malformed
	// metadata.
	RejectInvalidCorrelation bool
}

// ReadinessCheck is one named dependency required to accept new work.
type ReadinessCheck struct {
	// Name is the unique secret-safe dependency name.
	Name string
	// Run evaluates whether the dependency can accept new work. Calls may
	// overlap across requests; it must honor cancellation and use safe state.
	// Probe timeout bounds the response, not forced termination of the callback.
	Run func(context.Context) error
}

// Management configures the platform-owned operational listener.
type Management struct {
	// Address is bound by the platform. Empty with no Listener selects
	// 127.0.0.1:8081.
	Address string
	// Listener is borrowed until management startup acquires it. Rejected plans
	// leave it caller-owned; attempted server construction closes it on failure,
	// and successful construction transfers ownership to the runtime.
	Listener net.Listener
	// Details exposes configured check names (up to 128 bytes) and binary statuses;
	// callers must keep diagnostic names free of secrets.
	Details bool
	// TrustCorrelation authenticates an immediate peer before inbound
	// correlation metadata is preserved.
	TrustCorrelation func(*http.Request) bool
	// RejectInvalidCorrelation returns HTTP 400 instead of replacing malformed
	// metadata.
	RejectInvalidCorrelation bool
}

// ErrInvalidDefinition identifies a rejected service definition.
var ErrInvalidDefinition = errors.New("invalid service definition")

// DefinitionError identifies one invalid public definition field.
type DefinitionError struct {
	// Field identifies the rejected definition path.
	Field string
	// Reason safely describes the rejected contract.
	Reason string
}

// Error returns a safe definition diagnostic.
func (err *DefinitionError) Error() string {
	return fmt.Sprintf("%s: %s: %v", err.Field, err.Reason, ErrInvalidDefinition)
}

// Unwrap makes DefinitionError inspectable with errors.Is.
func (err *DefinitionError) Unwrap() error { return ErrInvalidDefinition }

// ConfigurationError identifies a selected command configuration failure.
type ConfigurationError struct {
	// Command identifies the selected command.
	Command string
	// Err retains the application configuration cause.
	Err error
}

// Error returns a safe configuration diagnostic without formatting its cause.
func (err *ConfigurationError) Error() string {
	return fmt.Sprintf("load %s configuration failed", err.Command)
}

// Unwrap returns the application configuration failure.
func (err *ConfigurationError) Unwrap() error { return err.Err }

// ConstructionError identifies selected command plan construction failure.
type ConstructionError struct {
	// Command identifies the selected command or runtime boundary.
	Command string
	// Err retains the application construction cause.
	Err error
}

// Error returns a safe construction diagnostic without formatting its cause.
func (err *ConstructionError) Error() string {
	return fmt.Sprintf("construct %s failed", err.Command)
}

// Unwrap returns the application construction failure.
func (err *ConstructionError) Unwrap() error { return err.Err }

// ShutdownTimeoutError identifies cleanup that exceeded its finite budget.
type ShutdownTimeoutError struct {
	// Err retains the deadline cause.
	Err error
}

// Error returns a stable shutdown-timeout diagnostic.
func (err *ShutdownTimeoutError) Error() string {
	return "service shutdown deadline exceeded"
}

// Unwrap returns the deadline cause.
func (err *ShutdownTimeoutError) Unwrap() error { return err.Err }

// Main invokes a definition with process arguments, environment, streams, and
// the platform default signal set. It returns an exit code and never calls
// os.Exit.
func Main(definition Definition) int {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, defaultSignals()...)
	defer signal.Stop(signals)

	return Execute(context.Background(), definition, Invocation{
		Args:        append([]string(nil), os.Args[1:]...),
		Environment: append([]string(nil), os.Environ()...),
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Signals:     signals,
	})
}

// Execute invokes one command without reading or mutating process globals.
// Nil ctx or writers returns 70 without dispatch. It copies invocation slices,
// validates the definition, and builds only the selected role. Borrowed callback
// state must not be mutated concurrently without caller synchronization.
//
// Exit codes are 0 for success or normal selected-command parent cancellation,
// 1 for ordinary one-shot failure, 2 for usage, 78 for configuration, 75 for
// startup/maintenance, 70 for construction
// or shutdown failure, 124 for shutdown deadline, and 130/143 for signals.
// Higher-priority shutdown classifications override joined signal/configuration
// failures. Diagnostics protect callback causes; direct errors and panic values
// obtained through lower-level APIs remain application-controlled.
func Execute(ctx context.Context, definition Definition, invocation Invocation) int {
	if ctx == nil || invocation.Stdout == nil || invocation.Stderr == nil {
		return exitSoftware
	}
	invocation.Args = append([]string(nil), invocation.Args...)
	invocation.Environment = append([]string(nil), invocation.Environment...)

	application, state, err := compileDefinition(definition, invocation)
	if err != nil {
		renderError(invocation.Stderr, err)

		return exitCode(err)
	}

	result := application.RunCommand(ctx, cli.Request{
		Args:           append([]string(nil), invocation.Args...),
		Stdout:         invocation.Stdout,
		Stderr:         invocation.Stderr,
		NonInteractive: true,
	})
	if result.Err == nil {
		return result.ExitCode
	}
	// Parent cancellation of a selected command is a clean exit only when
	// cleanup succeeded. go-cli reclassifies a handler error that matches the
	// cancellation and drops it from result.Err, so independent task, shutdown
	// or startup rollback failures are classified from the handler's own error.
	if state.selected != 0 && ctx.Err() != nil &&
		classificationIs(result.Err, context.Cause(ctx)) {
		if state.commandErr != nil && !isCancellationResult(ctx, state.commandErr) {
			fallback := exitSoftware
			if state.selected == CommandKindOneShot {
				fallback = exitCommand
			}
			return exitCodeWithDefault(state.commandErr, fallback)
		}

		return exitSuccess
	}

	if state.commandErr != nil && classificationIs(result.Err, state.commandErr) {
		// Classify the private callback result before CLI secret protection
		// hides its concrete type. Keep rendering and public causes protected.
		return exitCode(errors.Join(state.commandErr, result.Err))
	}

	return exitCode(result.Err)
}

type executionState struct {
	selected   CommandKind
	commandErr error
}

// The CLI classifies and formats callback errors with standard tree walks.
// Keep the original graph in executionState for service classification, while
// exposing bounded matching and a safe diagnostic to that boundary.
type commandResultError struct{ cause error }

func (*commandResultError) Error() string { return "service command failed" }

func (err *commandResultError) Is(target error) bool {
	return classificationIs(err.cause, target)
}

func (err *commandResultError) As(target any) bool {
	return classificationInto(err.cause, target)
}

type commandApplication interface {
	RunCommand(context.Context, cli.Request) cli.Result
}

type commandSignals struct {
	context            context.Context
	escalation         <-chan os.Signal
	beforeSignalCancel func(func())
	stop               func()
}

func coordinateCommandSignals(
	parent context.Context,
	signals <-chan os.Signal,
) commandSignals {
	if signals == nil {
		return commandSignals{
			context:            parent,
			beforeSignalCancel: func(func()) {},
			stop:               func() {},
		}
	}

	ctx, cancel := context.WithCancelCause(parent)
	escalation := make(chan os.Signal, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	var stopOnce sync.Once
	var callbackMu sync.Mutex
	var beforeCancel func()
	signalReceived := false
	runBeforeCancel := func() {
		callbackMu.Lock()
		signalReceived = true
		callback := beforeCancel
		callbackMu.Unlock()
		if callback != nil {
			callback()
		}
	}
	go func() {
		defer close(done)
		defer close(escalation)

		select {
		case received, open := <-signals:
			if !open {
				runBeforeCancel()
				cancel(ErrSignal)

				return
			}
			runBeforeCancel()
			cancel(&SignalError{Signal: received})
		case <-parent.Done():
			cancel(context.Cause(parent))
		case <-stop:
			return
		}

		select {
		case received, open := <-signals:
			if open {
				escalation <- received
			}
		case <-stop:
		}
	}()

	return commandSignals{
		context:    ctx,
		escalation: escalation,
		beforeSignalCancel: func(callback func()) {
			callbackMu.Lock()
			defer callbackMu.Unlock()
			if !signalReceived {
				beforeCancel = callback
			}
		},
		stop: func() {
			stopOnce.Do(func() { close(stop) })
			<-done
			cancel(context.Canceled)
		},
	}
}

func preserveCommandSignal(ctx context.Context, err error) error {
	cause := context.Cause(ctx)
	if !classificationIs(cause, ErrSignal) || classificationIs(err, cause) {
		return err
	}

	return errors.Join(err, cause)
}

func compileDefinition(
	definition Definition,
	invocation Invocation,
) (commandApplication, *executionState, error) {
	commands, err := definitionCommands(definition.Commands)
	if err != nil {
		return nil, nil, err
	}
	if err := validateIdentity(definition.Identity); err != nil {
		return nil, nil, err
	}
	if err := validateMaintenance(definition.Maintenance); err != nil {
		return nil, nil, err
	}
	if _, err := correlation.Disclose(
		"correlation.id",
		"validation",
		definition.CorrelationDisclosure,
	); err != nil {
		return nil, nil, &DefinitionError{
			Field: "CorrelationDisclosure", Reason: "is invalid",
		}
	}
	if len(commands) == 0 {
		return nil, nil, &DefinitionError{
			Field: "Commands", Reason: "must contain at least one command",
		}
	}
	switch definition.Maintenance.Store {
	case nil:
	default:
		for index, command := range commands {
			switch command.name {
			case "down", "up", "status":
				return nil, nil, &DefinitionError{
					Field:  fmt.Sprintf("Commands[%d].Name", index),
					Reason: "is reserved by maintenance mode",
				}
			}
		}
	}

	logger := definition.Logger
	factory := definition.Correlation
	if factory == nil {
		factory, _ = correlation.NewFactory(correlation.FactoryOptions{})
	}

	state := &executionState{}
	children := make([]cli.CommandSpec, 0, len(commands))
	for _, registered := range commands {
		command := registered
		handler := func(ctx context.Context, parsed cli.Invocation) error {
			state.selected = command.kind
			coordinated := coordinateCommandSignals(ctx, invocation.Signals)
			defer coordinated.stop()
			commandInvocation := invocation
			commandInvocation.input = parsed.Input()
			commandInvocation.Signals = coordinated.escalation
			commandInvocation.beforeSignalCancel = coordinated.beforeSignalCancel
			values, startErr := factory.Create()
			if startErr != nil {
				return &ConstructionError{Command: command.name, Err: startErr}
			}
			runtimeContext := correlation.WithValues(coordinated.context, values)
			identity := ProcessIdentity{
				Identity: definition.Identity,
				Role:     command.name,
			}
			observability := newRuntimeObservability(
				runtimeContext,
				identity,
				logger,
				definition.Observer,
				definition.CorrelationDisclosure,
			)
			buildLogger := logger
			if observability != nil {
				buildLogger = observability.logger
			}
			constructionContext, cancelConstruction := context.WithTimeout(
				runtimeContext,
				defaultStartupTimeout,
			)
			defer cancelConstruction()
			build := BuildContext{
				Identity:    identity,
				Logger:      buildLogger,
				Correlation: factory,
			}
			observability.event(
				constructionContext,
				RuntimeEventConstruction,
				RuntimeResultStarted,
				command.name,
				0,
				false,
			)
			constructionStarted := time.Now()
			plan, buildErr := command.build(
				constructionContext,
				commandInvocation,
				build,
			)
			cancelConstruction()
			if buildErr != nil {
				observability.event(
					runtimeContext,
					RuntimeEventConstruction,
					RuntimeResultFailed,
					command.name,
					time.Since(constructionStarted),
					false,
				)
				return preserveCommandSignal(runtimeContext, buildErr)
			}
			observability.event(
				runtimeContext,
				RuntimeEventConstruction,
				RuntimeResultSucceeded,
				command.name,
				time.Since(constructionStarted),
				false,
			)

			return preserveCommandSignal(runtimeContext, executePlan(
				runtimeContext,
				commandInvocation,
				definition,
				factory,
				command,
				plan,
				observability,
			))
		}
		children = append(children, cli.CommandSpec{
			Name:    command.name,
			Summary: command.summary,
			Options: command.options,
			Handler: handler,
		})
	}
	children = append(children, maintenanceCommandSpecs(definition, invocation, factory, state)...)
	for index := range children {
		handler := children[index].Handler
		children[index].Handler = func(ctx context.Context, invocation cli.Invocation) error {
			err := handler(ctx, invocation)
			state.commandErr = err
			if err == nil {
				return nil
			}
			return &commandResultError{cause: err}
		}
	}

	exitPolicy := cli.WithExitCodePolicy(cli.ExitCodePolicy{
		Usage: exitUsage, Command: exitCommand, Internal: exitSoftware,
	})
	application, err := cli.CompileCommandSet(
		cli.CommandSet{
			Name:     definition.Identity.Name,
			Version:  identityValue(definition.Identity.Version),
			Commands: children,
		},
		exitPolicy,
	)
	if err != nil {
		return nil, nil, err
	}

	return application, state, nil
}

func definitionCommands(commands Commands) ([]*commandRegistration, error) {
	standard := []struct {
		name    string
		kind    CommandKind
		command Command
	}{
		{
			name: "serve", kind: CommandKindLongRunning,
			command: commands.Serve,
		},
		{
			name: "worker", kind: CommandKindLongRunning,
			command: commands.Worker,
		},
		{
			name: "schedule", kind: CommandKindLongRunning,
			command: commands.Schedule,
		},
		{
			name: "migrate", kind: CommandKindOneShot,
			command: commands.Migrate,
		},
	}
	registered := make([]*commandRegistration, 0, len(standard)+len(commands.Custom))
	names := make(map[string]struct{})
	for _, item := range standard {
		if item.command.registration == nil {
			continue
		}
		if item.command.registration.name != item.name {
			return nil, &DefinitionError{
				Field:  "Commands." + item.name,
				Reason: "must register command " + item.name,
			}
		}
		if item.command.registration.kind != item.kind {
			return nil, &DefinitionError{
				Field:  "Commands." + item.name,
				Reason: "uses the wrong command kind",
			}
		}
		registered = append(registered, item.command.registration)
		names[item.name] = struct{}{}
	}
	for index, command := range commands.Custom {
		if command.registration == nil {
			return nil, &DefinitionError{
				Field: fmt.Sprintf("Commands.Custom[%d]", index), Reason: "is empty",
			}
		}
		name := command.registration.name
		if isStandardCommand(name) || name == "help" || name == "version" {
			return nil, &DefinitionError{
				Field:  fmt.Sprintf("Commands.Custom[%d]", index),
				Reason: "uses a reserved command name",
			}
		}
		if _, duplicate := names[name]; duplicate {
			return nil, &DefinitionError{
				Field:  fmt.Sprintf("Commands.Custom[%d]", index),
				Reason: "duplicates another command",
			}
		}
		registered = append(registered, command.registration)
		names[name] = struct{}{}
	}
	for index, command := range registered {
		if !validCommandName(command.name) {
			return nil, &DefinitionError{
				Field:  fmt.Sprintf("Commands[%d].Name", index),
				Reason: "must be lowercase kebab case",
			}
		}
		if command.kind != CommandKindLongRunning && command.kind != CommandKindOneShot {
			return nil, &DefinitionError{
				Field: fmt.Sprintf("Commands[%d].Kind", index), Reason: "is unknown",
			}
		}
		if command.invalid != "" {
			return nil, &DefinitionError{
				Field:  fmt.Sprintf("Commands[%d]", index),
				Reason: command.invalid,
			}
		}
	}

	return registered, nil
}

func isStandardCommand(name string) bool {
	return name == "serve" || name == "worker" ||
		name == "schedule" || name == "migrate"
}

func validateIdentity(identity Identity) error {
	if strings.TrimSpace(identity.Name) == "" ||
		!validCommandName(identity.Name) {
		return &DefinitionError{
			Field: "Identity.Name", Reason: "must be lowercase kebab case",
		}
	}
	if identity.Version != "" && !validSemanticVersion(identity.Version) {
		return &DefinitionError{
			Field: "Identity.Version", Reason: "must be semantic version syntax",
		}
	}
	if identity.Commit != "" && !validSourceRevision(identity.Commit) {
		return &DefinitionError{
			Field: "Identity.Commit", Reason: "must be a hexadecimal source revision",
		}
	}
	if identity.BuildTime != "" {
		if _, err := time.Parse(time.RFC3339, identity.BuildTime); err != nil {
			return &DefinitionError{
				Field: "Identity.BuildTime", Reason: "must use RFC3339 syntax",
			}
		}
	}

	return nil
}

func validSemanticVersion(version string) bool {
	coreAndPrerelease, build, hasBuild := strings.Cut(version, "+")
	if hasBuild && !validVersionIdentifiers(build, false) {
		return false
	}
	core, prerelease, hasPrerelease := strings.Cut(coreAndPrerelease, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if !validVersionNumber(part) {
			return false
		}
	}
	if hasPrerelease && !validVersionIdentifiers(prerelease, true) {
		return false
	}

	return true
}

func validCommandName(value string) bool {
	if !validRuntimeIdentity(value) || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	previousHyphen := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		if character == '-' {
			if previousHyphen || index == len(value)-1 {
				return false
			}
			previousHyphen = true

			continue
		}
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') {
			return false
		}
		previousHyphen = false
	}

	return true
}

func validSourceRevision(value string) bool {
	if len(value) < 7 || len(value) > 64 {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') &&
			(character < 'A' || character > 'F') {
			return false
		}
	}

	return true
}

func validVersionNumber(value string) bool {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return false
	}
	for index := range len(value) {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}

	return true
}

func validVersionIdentifiers(value string, rejectLeadingZero bool) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		numeric := true
		for index := range len(identifier) {
			character := identifier[index]
			if (character < '0' || character > '9') &&
				(character < 'A' || character > 'Z') &&
				(character < 'a' || character > 'z') &&
				character != '-' {
				return false
			}
			numeric = numeric && character >= '0' && character <= '9'
		}
		if rejectLeadingZero && numeric &&
			len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}

	return true
}

func executePlan(
	ctx context.Context,
	invocation Invocation,
	definition Definition,
	factory *correlation.Factory,
	command *commandRegistration,
	plan Plan,
	observations ...*runtimeObservability,
) error {
	observability := firstObservability(observations)
	plan = snapshotPlan(plan)
	if plan.ShutdownTimeout < 0 {
		return &DefinitionError{Field: "Plan.ShutdownTimeout", Reason: "must not be negative"}
	}
	if plan.ComponentStopTimeout < 0 {
		return &DefinitionError{Field: "Plan.ComponentStopTimeout", Reason: "must not be negative"}
	}
	if plan.StartupTimeout < 0 {
		return &DefinitionError{Field: "Plan.StartupTimeout", Reason: "must not be negative"}
	}
	if plan.RollbackTimeout < 0 {
		return &DefinitionError{Field: "Plan.RollbackTimeout", Reason: "must not be negative"}
	}
	shutdownTimeout := plan.ShutdownTimeout
	if shutdownTimeout == 0 {
		shutdownTimeout = defaultShutdownTimeout
	}
	if command.kind == CommandKindOneShot && plan.HTTP != nil {
		return &DefinitionError{
			Field: "Plan.HTTP", Reason: "is unavailable for one-shot commands",
		}
	}
	if err := validateTasks(plan.Tasks); err != nil {
		return err
	}
	components := append([]Component(nil), plan.Components...)
	var runtime *Service
	var maintenance *maintenanceRuntime
	switch command.kind {
	case CommandKindLongRunning:
		switch definition.Maintenance.Store {
		case nil:
		default:
			maintenance = newMaintenanceRuntime(definition.Maintenance, observability)
			components = append([]Component{maintenance.component()}, components...)
		}
	}
	availability := newPlatformState(func() *Service { return runtime }, maintenance)
	managementEnabled := command.kind == CommandKindLongRunning || plan.Management
	managementConfig := definition.Management
	if plan.ManagementConfig != nil {
		managementConfig = *plan.ManagementConfig
	}
	if managementEnabled {
		if err := validateReadiness(plan.Readiness); err != nil {
			return err
		}
		if err := validateManagement(managementConfig); err != nil {
			return err
		}
		if err := validateBusinessHTTP(plan.HTTP, managementConfig); err != nil {
			return err
		}
		management := newManagementOwner(
			managementConfig,
			factory,
			definition.TracePropagation,
			plan.Readiness,
			func() *Service { return runtime },
			availability,
			observability,
		)
		components = append([]Component{management.component()}, components...)
	}
	if command.kind == CommandKindLongRunning && plan.HTTP != nil {
		business := newBusinessOwner(
			*plan.HTTP,
			factory,
			definition.TracePropagation,
			maintenance,
		)
		business.observability = observability
		availability.business = business
		components = append(components, business.component())
		plan.Tasks = append([]Task{{
			Name: "service-business-http",
			Run:  business.run,
		}}, plan.Tasks...)
	}
	if maintenance != nil {
		plan.Tasks = append([]Task{maintenance.task()}, plan.Tasks...)
	}
	components = observeComponents(components, observability)

	runtimeConfig := Config{
		Components:      components,
		StartupTimeout:  plan.StartupTimeout,
		RollbackTimeout: plan.RollbackTimeout,
	}
	if plan.HTTP != nil {
		runtimeConfig.MaxTasks = len(plan.Tasks)
	}
	var err error
	runtime, err = New(runtimeConfig)
	if err != nil {
		return &ConstructionError{Command: command.name, Err: err}
	}
	runtime.componentStopTimeout = plan.ComponentStopTimeout
	if invocation.beforeSignalCancel != nil {
		invocation.beforeSignalCancel(func() { _ = runtime.Drain() })
	}
	observability.event(ctx, RuntimeEventStartup, RuntimeResultStarted, "", 0, true)
	startupStarted := time.Now()
	if err := runtime.Start(ctx); err != nil {
		observability.event(
			ctx, RuntimeEventStartup, RuntimeResultFailed, "", time.Since(startupStarted), true,
		)
		return err
	}
	observability.event(
		ctx, RuntimeEventStartup, RuntimeResultSucceeded, "", time.Since(startupStarted), true,
	)

	switch command.kind {
	case CommandKindLongRunning:
		return executeLongRunning(ctx, invocation, runtime, plan.Tasks, availability, shutdownTimeout, observability)
	default:
		return executeOneShot(ctx, invocation, runtime, plan.Tasks, availability, shutdownTimeout, observability)
	}
}

func executeOneShot(
	ctx context.Context,
	invocation Invocation,
	runtime *Service,
	tasks []Task,
	availability *platformState,
	shutdownTimeout time.Duration,
	observations ...*runtimeObservability,
) error {
	observability := firstObservability(observations)
	result := make(chan error, 1)
	if err := runtime.Go("one-shot command", func(taskContext context.Context) error {
		var taskErr error
		for _, task := range tasks {
			observability.event(taskContext, RuntimeEventTask, RuntimeResultStarted, task.Name, 0, false)
			started := time.Now()
			if err := invoke(task.Name, "run", task.Run, taskContext); err != nil {
				observability.event(
					taskContext, RuntimeEventTask, RuntimeResultFailed, task.Name, time.Since(started), false,
				)
				taskErr = &ComponentError{
					Component: task.Name, Operation: "run", Err: err,
				}
				break
			}
			observability.event(
				taskContext, RuntimeEventTask, RuntimeResultSucceeded, task.Name, time.Since(started), false,
			)
		}
		result <- taskErr

		return nil
	}); err != nil {
		return shutdownAfterFailure(runtime, err, shutdownTimeout, observability)
	}
	availability.Activate()
	observability.markReady(ctx)

	select {
	case taskErr := <-result:
		return errors.Join(taskErr, shutdownOneShot(runtime, shutdownTimeout, observability))
	case <-runtime.Context().Done():
		return finishCanceledOneShot(runtime, result, invocation.Signals, shutdownTimeout, observability)
	}
}

func finishCanceledOneShot(
	runtime *Service,
	result <-chan error,
	escalation <-chan os.Signal,
	shutdownTimeout time.Duration,
	observations ...*runtimeObservability,
) error {
	observability := firstObservability(observations)
	var shutdownErr error
	if escalation != nil {
		shutdownErr = observedShutdown(runtime, escalation, shutdownTimeout, observability)
	} else {
		shutdownErr = shutdownOneShot(runtime, shutdownTimeout, observability)
	}
	if shutdownErr != nil {
		return shutdownErr
	}

	taskErr := <-result
	if isCancellationResult(runtime.Context(), taskErr) {
		return context.Cause(runtime.Context())
	}

	return taskErr
}

func shutdownOneShot(runtime *Service, shutdownTimeout time.Duration, observations ...*runtimeObservability) error {
	observability := firstObservability(observations)
	observability.beginDrain(runtime.Context())
	started := time.Now()
	shutdownContext, cancel := newShutdownContext(runtime, shutdownTimeout)
	defer cancel()

	err := classifyShutdownError(runtime.Shutdown(shutdownContext))
	observability.finishShutdown(runtime.Context(), started, err)

	return err
}

func observedShutdown(
	runtime *Service,
	escalation <-chan os.Signal,
	shutdownTimeout time.Duration,
	observability *runtimeObservability,
) error {
	observability.beginDrain(runtime.Context())
	started := time.Now()
	err := shutdownWithEscalation(
		runtime,
		shutdownTimeout,
		escalation,
	)
	observability.finishShutdown(runtime.Context(), started, err)

	return err
}

func executeLongRunning(
	ctx context.Context,
	invocation Invocation,
	runtime *Service,
	tasks []Task,
	availability *platformState,
	shutdownTimeout time.Duration,
	observations ...*runtimeObservability,
) error {
	observability := firstObservability(observations)
	var httpStopped <-chan struct{}
	if availability != nil && availability.business != nil {
		httpStopped = availability.business.stopped
	}
	for _, task := range tasks {
		current := task
		if err := runtime.Go(current.Name, func(taskContext context.Context) error {
			observability.event(taskContext, RuntimeEventTask, RuntimeResultStarted, current.Name, 0, false)
			started := time.Now()
			err := invoke(current.Name, "run", current.Run, taskContext)
			if err == nil && taskContext.Err() == nil {
				err = errors.New("long-running task exited without cancellation")
			}
			result := RuntimeResultSucceeded
			if err != nil && !isCancellationResult(taskContext, err) {
				result = RuntimeResultFailed
			}
			observability.event(taskContext, RuntimeEventTask, result, current.Name, time.Since(started), false)

			return err
		}); err != nil {
			return shutdownAfterFailure(runtime, err, shutdownTimeout, observability)
		}
	}
	if availability != nil {
		availability.Activate()
	}
	observability.markReady(ctx)

	var waitErr error
	if invocation.Signals != nil {
		select {
		case <-ctx.Done():
		case <-runtime.Context().Done():
		case <-httpStopped:
		}
		waitErr = observedShutdown(runtime, invocation.Signals, shutdownTimeout, observability)
	} else {
		select {
		case <-ctx.Done():
		case <-runtime.Context().Done():
		case <-httpStopped:
		}
		shutdownContext, cancel := newShutdownContext(runtime, shutdownTimeout)
		observability.beginDrain(runtime.Context())
		started := time.Now()
		waitErr = classifyShutdownError(runtime.Shutdown(shutdownContext))
		observability.finishShutdown(runtime.Context(), started, waitErr)
		cancel()
	}
	if waitErr != nil {
		if availability != nil && availability.business != nil {
			return errors.Join(waitErr, availability.business.failure())
		}
		return waitErr
	}

	cause := context.Cause(runtime.Context())
	if signalError, ok := classificationAs[*SignalError](cause); ok {
		return signalError
	}

	return nil
}

func shutdownAfterFailure(
	runtime *Service,
	primary error,
	shutdownTimeout time.Duration,
	observations ...*runtimeObservability,
) error {
	observability := firstObservability(observations)
	observability.beginDrain(runtime.Context())
	started := time.Now()
	shutdownContext, cancel := newShutdownContext(runtime, shutdownTimeout)
	defer cancel()

	shutdownErr := classifyShutdownError(runtime.Shutdown(shutdownContext))
	observability.finishShutdown(runtime.Context(), started, shutdownErr)

	return errors.Join(primary, shutdownErr)
}

// snapshotPlan isolates registration containers, not referenced resources.
// Deep-copying handlers or callbacks would invent a different ownership model.
func snapshotPlan(plan Plan) Plan {
	snapshot := plan
	snapshot.Components = append([]Component(nil), plan.Components...)
	snapshot.Tasks = append([]Task(nil), plan.Tasks...)
	snapshot.Readiness = append([]ReadinessCheck(nil), plan.Readiness...)
	if plan.HTTP != nil {
		businessHTTP := *plan.HTTP
		businessHTTP.Options = append([]serverhttp.Option(nil), plan.HTTP.Options...)
		snapshot.HTTP = &businessHTTP
	}
	if plan.ManagementConfig != nil {
		management := *plan.ManagementConfig
		snapshot.ManagementConfig = &management
	}

	return snapshot
}

const defaultManagementAddress = "127.0.0.1:8081"
const defaultManagementConnectionLimit = 256
const defaultMaxReadinessChecks = 64
const managementReadHeaderTimeout time.Duration = 2_000_000_000
const managementReadTimeout time.Duration = 5_000_000_000
const managementWriteTimeout time.Duration = 5_000_000_000
const managementIdleTimeout time.Duration = 30_000_000_000
const managementShutdownTimeout time.Duration = 5_000_000_000

type platformState struct {
	runtime     func() *Service
	activated   atomic.Bool
	maintenance *maintenanceRuntime
	business    *businessOwner
}

func newPlatformState(
	runtime func() *Service,
	maintenance ...*maintenanceRuntime,
) *platformState {
	return &platformState{runtime: runtime, maintenance: firstMaintenanceRuntime(maintenance)}
}

func (state *platformState) Activate() {
	state.activated.Store(true)
}

func (state *platformState) StartupComplete() bool {
	return state.activated.Load() && state.runtime().StartupComplete()
}

func (state *platformState) Ready() bool {
	return state.activated.Load() && !state.maintenance.enabled() && state.runtime().Ready()
}

type managementOwner struct {
	config        Management
	factory       *correlation.Factory
	trace         serverhttp.Middleware
	readiness     []ReadinessCheck
	runtime       func() *Service
	availability  *platformState
	observability *runtimeObservability
	server        *serverhttp.Server
	cancel        context.CancelFunc
	done          chan error
}

type businessOwner struct {
	config        HTTP
	factory       *correlation.Factory
	trace         serverhttp.Middleware
	maintenance   *maintenanceRuntime
	observability *runtimeObservability
	server        *serverhttp.Server
	stopped       chan struct{}
	// runErr is published by closing stopped before waiting for handlers.
	runErr error
}

func newManagementOwner(
	config Management,
	factory *correlation.Factory,
	trace serverhttp.Middleware,
	readiness []ReadinessCheck,
	runtime func() *Service,
	availability *platformState,
	observations ...*runtimeObservability,
) *managementOwner {
	observability := firstObservability(observations)
	return &managementOwner{
		config: config, factory: factory, trace: trace,
		readiness: append([]ReadinessCheck(nil), readiness...),
		runtime:   runtime, availability: availability,
		observability: observability,
	}
}

func (owner *managementOwner) component() Component {
	return Component{
		Name:  "service-management",
		Start: owner.start,
		Stop:  owner.stop,
	}
}

func (owner *managementOwner) start(ctx context.Context) error {
	runtime := owner.runtime()
	checks := make([]healthhttp.Check, 0, len(owner.readiness))
	for _, check := range owner.readiness {
		checks = append(checks, healthhttp.Check{Name: check.Name, Run: check.Run})
	}
	probes, err := healthhttp.New(healthhttp.Config{
		Lifecycle: owner.availability,
		Checks:    checks,
		Details:   owner.config.Details,
		Observer: healthhttp.ObserverFunc(func(
			ctx context.Context,
			observation healthhttp.Observation,
		) {
			owner.observability.probe(
				ctx, observation.Probe, observation.Available, observation.Duration,
			)
		}),
	})
	if err != nil {
		return &ConstructionError{Command: "management probes", Err: err}
	}
	router := http.NewServeMux()
	router.Handle("/livez", probes.Liveness())
	router.Handle("/startupz", probes.Startup())
	router.Handle("/readyz", probes.Readiness())

	handler := http.Handler(router)
	invalid := httpcorrelation.ReplaceInvalid
	if owner.config.RejectInvalidCorrelation {
		invalid = httpcorrelation.RejectInvalid
	}
	options := []serverhttp.Option{
		serverhttp.WithCorrelation(owner.factory, httpcorrelation.Options{
			Invalid: invalid,
			Trust:   owner.config.TrustCorrelation,
		}),
		serverhttp.WithReadHeaderTimeout(managementReadHeaderTimeout),
		serverhttp.WithReadTimeout(managementReadTimeout),
		serverhttp.WithWriteTimeout(managementWriteTimeout),
		serverhttp.WithIdleTimeout(managementIdleTimeout),
		serverhttp.WithShutdownTimeout(managementShutdownTimeout),
		serverhttp.WithMaxHeaderBytes(16 << 10),
		serverhttp.WithMaxConnections(defaultManagementConnectionLimit),
		serverhttp.WithBodyLimit(0),
	}
	if owner.trace != nil {
		options = append(options, serverhttp.WithIngressMiddleware(owner.trace))
	}

	listener := owner.config.Listener
	if listener == nil {
		address := resolvedManagementAddress(owner.config.Address)
		var listenConfig net.ListenConfig
		listener, err = listenConfig.Listen(ctx, "tcp", address)
		if err != nil {
			return &ConstructionError{Command: "management", Err: err}
		}
	}
	transferred := false
	defer func() {
		if !transferred {
			_ = listener.Close()
		}
	}()
	server, err := serverhttp.New(
		listener,
		handler,
		options...,
	)
	if err != nil {
		return &ConstructionError{Command: "management", Err: err}
	}
	owner.server = server
	runContext, cancel := context.WithCancel(context.Background())
	owner.cancel = cancel
	owner.done = make(chan error, 1)
	go func() {
		runErr := server.Run(runContext)
		if runErr != nil {
			runtime.cancelWithCause(&ComponentError{
				Component: "service-management",
				Operation: "run",
				Err:       runErr,
			})
		}
		owner.done <- runErr
	}()
	transferred = true

	return nil
}

func resolvedManagementAddress(address string) string {
	if address == "" {
		return defaultManagementAddress
	}

	return address
}

func (owner *managementOwner) stop(ctx context.Context) error {
	owner.cancel()

	return stopHTTPServer(ctx, owner.server, owner.done)
}

func newBusinessOwner(
	config HTTP,
	factory *correlation.Factory,
	trace serverhttp.Middleware,
	maintenance ...*maintenanceRuntime,
) *businessOwner {
	owner := &businessOwner{
		config: config, factory: factory, trace: trace,
		maintenance: firstMaintenanceRuntime(maintenance),
	}
	if config.JoinHandlers {
		owner.stopped = make(chan struct{})
	}
	return owner
}

func (owner *businessOwner) component() Component {
	component := Component{
		Name:  "service-business-http-owner",
		Start: owner.start,
		Stop:  owner.stop,
	}
	if owner.config.JoinHandlers {
		component.CloseAdmission = func() error { return owner.server.CloseAdmission() }
	}
	return component
}

func (owner *businessOwner) start(ctx context.Context) error {
	invalid := httpcorrelation.ReplaceInvalid
	if owner.config.RejectInvalidCorrelation {
		invalid = httpcorrelation.RejectInvalid
	}
	options := []serverhttp.Option{
		serverhttp.WithCorrelation(owner.factory, httpcorrelation.Options{
			Invalid: invalid,
			Trust:   owner.config.TrustCorrelation,
		}),
	}
	if owner.trace != nil {
		options = append(options, serverhttp.WithIngressMiddleware(owner.trace))
	}
	if owner.observability != nil {
		options = append(
			options,
			serverhttp.WithIngressMiddleware(owner.observability.request),
		)
	}
	if owner.maintenance != nil {
		options = append(
			options,
			serverhttp.WithIngressMiddleware(owner.maintenance.admission),
		)
	}
	options = append(options, owner.config.Options...)
	options = append(options, serverhttp.WithMiddleware(securityHeaders()))
	// The platform setting owns the task lifetime, so a low-level option must
	// not create a second, unjoined handler owner or disable the selected join.
	options = append(options, serverhttp.WithHandlerJoin(owner.config.JoinHandlers))

	listener := owner.config.Listener
	var err error
	if listener == nil {
		var listenConfig net.ListenConfig
		listener, err = listenConfig.Listen(ctx, "tcp", owner.config.Address)
		if err != nil {
			return &ConstructionError{Command: "business HTTP", Err: err}
		}
	}
	transferred := false
	defer func() {
		if !transferred {
			_ = listener.Close()
		}
	}()
	server, err := serverhttp.New(listener, owner.config.Handler, options...)
	if err != nil {
		return &ConstructionError{Command: "business HTTP", Err: err}
	}
	owner.server = server
	transferred = true

	return nil
}

func (owner *businessOwner) run(ctx context.Context) error {
	if !owner.config.JoinHandlers {
		return owner.server.Run(ctx)
	}
	runErr := invoke("service-business-http", "run", owner.server.Run, ctx)
	closeErr := invoke("service-business-http", "close", func(context.Context) error {
		return owner.server.Close()
	}, ctx)
	_ = owner.server.CloseAdmission()
	if runErr == nil && ctx.Err() == nil {
		runErr = errors.New("business HTTP stopped before cancellation")
	}
	owner.runErr = errors.Join(runErr, closeErr)
	// Notify the command before joining: a serving failure must start drain
	// and its finite overall allowance even while handler cleanup is held.
	close(owner.stopped)
	_ = owner.server.WaitHandlers(context.Background())
	return owner.runErr
}

func (owner *businessOwner) failure() error {
	select {
	case <-owner.stopped:
		return owner.runErr
	default:
		return nil
	}
}

func (owner *businessOwner) stop(context.Context) error {
	return owner.server.Close()
}

func stopHTTPServer(
	ctx context.Context,
	server *serverhttp.Server,
	done <-chan error,
) error {
	select {
	case <-ctx.Done():
		closeErr := server.Close()
		runErr := <-done

		return errors.Join(context.Cause(ctx), closeErr, runErr)
	default:
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		closeErr := server.Close()
		runErr := <-done

		return errors.Join(context.Cause(ctx), closeErr, runErr)
	}
}

func securityHeaders() serverhttp.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("X-Content-Type-Options", "nosniff")
			next.ServeHTTP(writer, request)
		})
	}
}

func validateManagement(config Management) error {
	if config.Address != "" && config.Listener != nil {
		return &DefinitionError{
			Field: "Management", Reason: "address and listener are mutually exclusive",
		}
	}

	return nil
}

func validateBusinessHTTP(config *HTTP, management Management) error {
	if config == nil {
		return nil
	}
	if (config.Address == "") == (config.Listener == nil) {
		return &DefinitionError{
			Field: "HTTP", Reason: "requires exactly one address or listener",
		}
	}
	if config.Handler == nil {
		return &DefinitionError{Field: "HTTP.Handler", Reason: "must not be nil"}
	}
	if config.Listener != nil && management.Listener != nil &&
		(config.Listener == management.Listener ||
			config.Listener.Addr().String() == management.Listener.Addr().String()) {
		return &DefinitionError{
			Field: "HTTP.Listener", Reason: "collides with management listener",
		}
	}
	managementAddress := management.Address
	if managementAddress == "" {
		if management.Listener == nil {
			managementAddress = defaultManagementAddress
		}
	}
	if config.Address != "" {
		if config.Address == managementAddress {
			return &DefinitionError{
				Field: "HTTP.Address", Reason: "collides with management address",
			}
		}
	}

	return nil
}

func validateReadiness(checks []ReadinessCheck) error {
	if len(checks) > defaultMaxReadinessChecks {
		return &DefinitionError{
			Field: "Plan.Readiness", Reason: "exceeds the maximum check count",
		}
	}
	names := make(map[string]struct{}, len(checks))
	for index, check := range checks {
		if !validRuntimeIdentity(check.Name) || check.Run == nil {
			return &DefinitionError{
				Field: fmt.Sprintf("Plan.Readiness[%d]", index),
				Reason: fmt.Sprintf(
					"requires a 1 to %d byte name and run callback",
					MaxRuntimeIdentityBytes,
				),
			}
		}
		if _, duplicate := names[check.Name]; duplicate {
			return &DefinitionError{
				Field:  fmt.Sprintf("Plan.Readiness[%d].Name", index),
				Reason: "must be unique",
			}
		}
		names[check.Name] = struct{}{}
	}

	return nil
}

func validateTasks(tasks []Task) error {
	if len(tasks) > defaultMaxTasks {
		return &DefinitionError{
			Field: "Plan.Tasks", Reason: "exceeds the maximum supervised task count",
		}
	}
	names := make(map[string]struct{}, len(tasks))
	for index, task := range tasks {
		if !validRuntimeIdentity(task.Name) || task.Run == nil {
			return &DefinitionError{
				Field: fmt.Sprintf("Plan.Tasks[%d]", index),
				Reason: fmt.Sprintf(
					"requires a 1 to %d byte name and run callback",
					MaxRuntimeIdentityBytes,
				),
			}
		}
		if _, duplicate := names[task.Name]; duplicate {
			return &DefinitionError{
				Field:  fmt.Sprintf("Plan.Tasks[%d].Name", index),
				Reason: "must be unique",
			}
		}
		names[task.Name] = struct{}{}
	}

	return nil
}

func classifyShutdownError(err error) error {
	if classificationIs(err, context.DeadlineExceeded) {
		return &ShutdownTimeoutError{Err: err}
	}

	return err
}

func exitCode(err error) int {
	return exitCodeWithDefault(err, exitSoftware)
}

func exitCodeWithDefault(err error, fallback int) int {
	if _, ok := classificationAs[*ShutdownTimeoutError](err); ok {
		return 124
	}
	if _, ok := classificationAs[*ShutdownError](err); ok {
		return exitSoftware
	}
	if signalError, ok := classificationAs[*SignalError](err); ok {
		if signalError.Signal == os.Interrupt {
			return 130
		}

		return 143
	}
	if _, ok := classificationAs[*ConfigurationError](err); ok {
		return exitConfiguration
	}
	if classificationIs(err, ErrInvalidDefinition) {
		return exitSoftware
	}
	if _, ok := classificationAs[*StartupError](err); ok {
		return exitTemporary
	}
	if _, ok := classificationAs[*MaintenanceError](err); ok {
		return exitTemporary
	}
	if _, ok := classificationAs[*ConstructionError](err); ok {
		return exitSoftware
	}
	if cliError, ok := classificationAs[*cli.Error](err); ok {
		switch cliError.Kind() {
		case cli.ErrorKindHelp, cli.ErrorKindVersion:
			return exitSuccess
		case cli.ErrorKindUnknownCommand, cli.ErrorKindUnknownOption,
			cli.ErrorKindMissingValue, cli.ErrorKindUsage,
			cli.ErrorKindMalformedValue:
			return exitUsage
		case cli.ErrorKindCommand:
			return exitCommand
		default:
			return exitSoftware
		}
	}

	return fallback
}

func renderError(writer io.Writer, err error) {
	_, _ = fmt.Fprintln(writer, err)
}

func identityValue(value string) string {
	if value == "" {
		return "unknown"
	}

	return value
}
