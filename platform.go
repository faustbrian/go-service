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

	"github.com/faustbrian/golib/pkg/cli"
	"github.com/faustbrian/golib/pkg/correlation"
	httpcorrelation "github.com/faustbrian/golib/pkg/correlation/http"
	"github.com/faustbrian/golib/pkg/service/healthhttp"
	"github.com/faustbrian/golib/pkg/service/serverhttp"
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

// Definition declares the complete immutable process construction surface.
type Definition struct {
	// Identity identifies the deployable service.
	Identity Identity
	// Commands declares every supported process role.
	Commands Commands
	// Logger is caller owned. Nil keeps logging disabled.
	Logger *slog.Logger
	// Correlation is caller owned. Nil selects the correlation default.
	Correlation *correlation.Factory
	// TracePropagation optionally extracts caller-owned trace context after
	// correlation.
	TracePropagation serverhttp.Middleware
	// Management configures the platform-owned operational listener.
	Management Management
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
	// Options declares bounded command-specific CLI input. Configuration
	// loaders retain the immutable raw invocation for application validation.
	Options []cli.OptionDefinition
	// Load decodes and validates command-specific configuration.
	Load func(context.Context, Invocation) (C, error)
	// Build constructs the owned runtime plan from typed configuration.
	Build func(context.Context, BuildContext, C) (Plan, error)
}

// CommandFor erases only the immutable command registration while preserving
// concrete configuration in application callbacks.
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

// Invocation is an immutable in-process command request.
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
	Signals <-chan os.Signal
}

// BuildContext contains immutable platform-owned construction values.
type BuildContext struct {
	// Identity identifies the service and selected process role.
	Identity ProcessIdentity
	// Logger is the optional caller-owned logger selected by the definition.
	Logger *slog.Logger
	// Correlation creates identifiers for platform-managed work boundaries.
	Correlation *correlation.Factory
}

// Plan declares resources and work owned by the selected command.
type Plan struct {
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

// Task is one named owned unit of application work.
type Task struct {
	// Name is the unique secret-safe diagnostic name.
	Name string
	// Run executes owned work and must honor context cancellation.
	Run func(context.Context) error
}

// HTTP declares one caller-owned business handler and listener boundary.
type HTTP struct {
	// Address is bound by the platform. Exactly one of Address and Listener is
	// required.
	Address string
	// Listener transfers ownership after successful plan validation.
	Listener net.Listener
	// Handler owns application routing and protocol behavior.
	Handler http.Handler
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
	// Run evaluates whether the dependency can accept new work.
	Run func(context.Context) error
}

// Management configures the platform-owned operational listener.
type Management struct {
	// Address is bound by the platform. Empty with no Listener selects
	// 127.0.0.1:8081.
	Address string
	// Listener transfers ownership after successful plan validation.
	Listener net.Listener
	// Details enables bounded check names and binary statuses.
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
	if state.selected != 0 && ctx.Err() != nil &&
		errors.Is(result.Err, context.Cause(ctx)) {
		return exitSuccess
	}

	return exitCode(result.Err)
}

type executionState struct {
	selected CommandKind
}

type commandApplication interface {
	RunCommand(context.Context, cli.Request) cli.Result
}

type commandSignals struct {
	context    context.Context
	escalation <-chan os.Signal
	stop       func()
}

func coordinateCommandSignals(
	parent context.Context,
	signals <-chan os.Signal,
) commandSignals {
	if signals == nil {
		return commandSignals{
			context: parent,
			stop:    func() {},
		}
	}

	ctx, cancel := context.WithCancelCause(parent)
	escalation := make(chan os.Signal, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		defer close(done)
		defer close(escalation)

		select {
		case received, open := <-signals:
			if !open {
				cancel(ErrSignal)

				return
			}
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
		stop: func() {
			stopOnce.Do(func() { close(stop) })
			<-done
			cancel(context.Canceled)
		},
	}
}

func preserveCommandSignal(ctx context.Context, err error) error {
	cause := context.Cause(ctx)
	if !errors.Is(cause, ErrSignal) || errors.Is(err, cause) {
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
	if len(commands) == 0 {
		return nil, nil, &DefinitionError{
			Field: "Commands", Reason: "must contain at least one command",
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
		handler := func(ctx context.Context, _ cli.Invocation) error {
			state.selected = command.kind
			coordinated := coordinateCommandSignals(ctx, invocation.Signals)
			defer coordinated.stop()
			commandInvocation := invocation
			commandInvocation.Signals = coordinated.escalation
			values, startErr := factory.Start()
			if startErr != nil {
				return &ConstructionError{Command: command.name, Err: startErr}
			}
			runtimeContext := correlation.WithValues(coordinated.context, values)
			constructionContext, cancelConstruction := context.WithTimeout(
				runtimeContext,
				defaultStartupTimeout,
			)
			defer cancelConstruction()
			build := BuildContext{
				Identity: ProcessIdentity{
					Identity: definition.Identity,
					Role:     command.name,
				},
				Logger:      logger,
				Correlation: factory,
			}
			plan, buildErr := command.build(
				constructionContext,
				commandInvocation,
				build,
			)
			cancelConstruction()
			if buildErr != nil {
				return preserveCommandSignal(runtimeContext, buildErr)
			}

			return preserveCommandSignal(runtimeContext, executePlan(
				runtimeContext,
				commandInvocation,
				definition,
				factory,
				command,
				plan,
			))
		}
		children = append(children, cli.CommandSpec{
			Name:    command.name,
			Summary: command.summary,
			Options: command.options,
			Handler: handler,
		})
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
	if value == "" || value[0] < 'a' || value[0] > 'z' {
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
) error {
	plan = snapshotPlan(plan)
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
	availability := newPlatformState(func() *Service { return runtime })
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
		)
		components = append([]Component{management.component()}, components...)
	}
	if command.kind == CommandKindLongRunning && plan.HTTP != nil {
		business := newBusinessOwner(
			*plan.HTTP,
			factory,
			definition.TracePropagation,
		)
		components = append(components, business.component())
		plan.Tasks = append([]Task{{
			Name: "service-business-http",
			Run:  business.run,
		}}, plan.Tasks...)
	}

	runtimeConfig := Config{Components: components}
	if plan.HTTP != nil {
		runtimeConfig.MaxTasks = len(plan.Tasks)
	}
	var err error
	runtime, err = New(runtimeConfig)
	if err != nil {
		return &ConstructionError{Command: command.name, Err: err}
	}
	if err := runtime.Start(ctx); err != nil {
		return err
	}

	if command.kind == CommandKindLongRunning {
		return executeLongRunning(ctx, invocation, runtime, plan.Tasks, availability)
	}

	return executeOneShot(ctx, invocation, runtime, plan.Tasks, availability)
}

func executeOneShot(
	ctx context.Context,
	invocation Invocation,
	runtime *Service,
	tasks []Task,
	availability *platformState,
) error {
	result := make(chan error, 1)
	if err := runtime.Go("one-shot command", func(taskContext context.Context) error {
		var taskErr error
		for _, task := range tasks {
			if err := invoke(task.Name, "run", task.Run, taskContext); err != nil {
				taskErr = &ComponentError{
					Component: task.Name, Operation: "run", Err: err,
				}
				break
			}
		}
		result <- taskErr

		return nil
	}); err != nil {
		return shutdownAfterFailure(runtime, err)
	}
	availability.Activate()

	select {
	case taskErr := <-result:
		return errors.Join(taskErr, shutdownOneShot(runtime))
	case <-runtime.Context().Done():
		return finishCanceledOneShot(runtime, result, invocation.Signals)
	}
}

func finishCanceledOneShot(
	runtime *Service,
	result <-chan error,
	escalation <-chan os.Signal,
) error {
	var shutdownErr error
	if escalation != nil {
		shutdownErr = shutdownWithEscalation(
			runtime,
			defaultShutdownTimeout,
			escalation,
		)
	} else {
		shutdownErr = shutdownOneShot(runtime)
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

func shutdownOneShot(runtime *Service) error {
	shutdownContext, cancel := context.WithTimeout(
		context.Background(),
		defaultShutdownTimeout,
	)
	defer cancel()

	return classifyShutdownError(runtime.Shutdown(shutdownContext))
}

func executeLongRunning(
	ctx context.Context,
	invocation Invocation,
	runtime *Service,
	tasks []Task,
	availability *platformState,
) error {
	for _, task := range tasks {
		current := task
		if err := runtime.Go(current.Name, func(taskContext context.Context) error {
			err := current.Run(taskContext)
			if err == nil && taskContext.Err() == nil {
				return errors.New("long-running task exited without cancellation")
			}

			return err
		}); err != nil {
			return shutdownAfterFailure(runtime, err)
		}
	}
	if availability != nil {
		availability.Activate()
	}

	var waitErr error
	if invocation.Signals != nil {
		select {
		case <-ctx.Done():
		case <-runtime.Context().Done():
		}
		waitErr = shutdownWithEscalation(
			runtime,
			defaultShutdownTimeout,
			invocation.Signals,
		)
	} else {
		select {
		case <-ctx.Done():
		case <-runtime.Context().Done():
		}
		shutdownContext, cancel := context.WithTimeout(
			context.Background(),
			defaultShutdownTimeout,
		)
		waitErr = classifyShutdownError(runtime.Shutdown(shutdownContext))
		cancel()
	}
	if waitErr != nil {
		return waitErr
	}

	cause := context.Cause(runtime.Context())
	if signalError, ok := errors.AsType[*SignalError](cause); ok {
		return signalError
	}

	return nil
}

func shutdownAfterFailure(runtime *Service, primary error) error {
	shutdownContext, cancel := context.WithTimeout(
		context.Background(),
		defaultShutdownTimeout,
	)
	defer cancel()

	return errors.Join(primary, classifyShutdownError(runtime.Shutdown(shutdownContext)))
}

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
	runtime   func() *Service
	activated atomic.Bool
}

func newPlatformState(runtime func() *Service) *platformState {
	return &platformState{runtime: runtime}
}

func (state *platformState) Activate() {
	state.activated.Store(true)
}

func (state *platformState) StartupComplete() bool {
	return state.activated.Load() && state.runtime().StartupComplete()
}

func (state *platformState) Ready() bool {
	return state.activated.Load() && state.runtime().Ready()
}

type managementOwner struct {
	config       Management
	factory      *correlation.Factory
	trace        serverhttp.Middleware
	readiness    []ReadinessCheck
	runtime      func() *Service
	availability *platformState
	server       *serverhttp.Server
	cancel       context.CancelFunc
	done         chan error
}

type businessOwner struct {
	config  HTTP
	factory *correlation.Factory
	trace   serverhttp.Middleware
	server  *serverhttp.Server
}

func newManagementOwner(
	config Management,
	factory *correlation.Factory,
	trace serverhttp.Middleware,
	readiness []ReadinessCheck,
	runtime func() *Service,
	availability *platformState,
) *managementOwner {
	return &managementOwner{
		config: config, factory: factory, trace: trace,
		readiness: append([]ReadinessCheck(nil), readiness...),
		runtime:   runtime, availability: availability,
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
	listener = limitConnections(listener, defaultManagementConnectionLimit)
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
) *businessOwner {
	return &businessOwner{config: config, factory: factory, trace: trace}
}

func (owner *businessOwner) component() Component {
	return Component{
		Name:  "service-business-http-owner",
		Start: owner.start,
		Stop:  owner.stop,
	}
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
	options = append(options, owner.config.Options...)
	options = append(options, serverhttp.WithMiddleware(securityHeaders()))

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
	return owner.server.Run(ctx)
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
		if strings.TrimSpace(check.Name) == "" || check.Run == nil {
			return &DefinitionError{
				Field:  fmt.Sprintf("Plan.Readiness[%d]", index),
				Reason: "requires a name and run callback",
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
		if strings.TrimSpace(task.Name) == "" || task.Run == nil {
			return &DefinitionError{
				Field:  fmt.Sprintf("Plan.Tasks[%d]", index),
				Reason: "requires a name and run callback",
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

type limitedListener struct {
	net.Listener
	capacity chan struct{}
	closed   chan struct{}
	close    sync.Once
}

func limitConnections(listener net.Listener, maximum int) net.Listener {
	return &limitedListener{
		Listener: listener,
		capacity: make(chan struct{}, maximum),
		closed:   make(chan struct{}),
	}
}

func (listener *limitedListener) Accept() (net.Conn, error) {
	select {
	case listener.capacity <- struct{}{}:
	case <-listener.closed:
		return nil, net.ErrClosed
	}
	connection, err := listener.Listener.Accept()
	if err != nil {
		<-listener.capacity

		return nil, err
	}

	return &limitedConnection{
		Conn: connection,
		release: func() {
			<-listener.capacity
		},
	}, nil
}

func (listener *limitedListener) Close() error {
	listener.close.Do(func() {
		close(listener.closed)
	})

	return listener.Listener.Close()
}

type limitedConnection struct {
	net.Conn
	once    sync.Once
	release func()
}

func (connection *limitedConnection) Close() error {
	err := connection.Conn.Close()
	connection.once.Do(connection.release)

	return err
}

func classifyShutdownError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &ShutdownTimeoutError{Err: err}
	}

	return err
}

func exitCode(err error) int {
	if _, ok := errors.AsType[*ShutdownTimeoutError](err); ok {
		return 124
	}
	if _, ok := errors.AsType[*ShutdownError](err); ok {
		return exitSoftware
	}
	if signalError, ok := errors.AsType[*SignalError](err); ok {
		if signalError.Signal == os.Interrupt {
			return 130
		}

		return 143
	}
	if _, ok := errors.AsType[*ConfigurationError](err); ok {
		return exitConfiguration
	}
	if errors.Is(err, ErrInvalidDefinition) {
		return exitSoftware
	}
	if _, ok := errors.AsType[*StartupError](err); ok {
		return exitTemporary
	}
	if _, ok := errors.AsType[*ConstructionError](err); ok {
		return exitSoftware
	}
	if cliError, ok := errors.AsType[*cli.Error](err); ok {
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

	return exitSoftware
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
