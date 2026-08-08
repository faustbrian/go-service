package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/faustbrian/golib/pkg/cli"
	"github.com/faustbrian/golib/pkg/correlation"
)

const (
	defaultMaintenanceRefreshInterval  = time.Second
	defaultMaintenanceOperationTimeout = time.Second
	maximumMaintenanceStateBytes       = 8 << 10
	maximumMaintenanceDuration         = 7 * 24 * time.Hour
	maintenanceCookieName              = "service_maintenance"
)

// ErrMaintenance identifies a maintenance state or store failure.
var ErrMaintenance = errors.New("service maintenance failure")

// MaintenanceError identifies one safe maintenance operation failure.
type MaintenanceError struct {
	// Operation is the bounded operation name.
	Operation string
	// Err preserves the cause without formatting it.
	Err error
}

// Error returns a secret-safe maintenance diagnostic.
func (err *MaintenanceError) Error() string {
	operation := "operation"
	switch err.Operation {
	case "load", "enable", "disable", "status":
		operation = err.Operation
	}

	return fmt.Sprintf("maintenance %s failed: %v", operation, ErrMaintenance)
}

// Unwrap preserves both the stable classification and the operation cause.
func (err *MaintenanceError) Unwrap() []error { return []error{ErrMaintenance, err.Err} }

// MaintenanceState is one immutable maintenance publication.
type MaintenanceState struct {
	// Enabled controls business admission and readiness.
	Enabled bool
	// Since records when maintenance was enabled.
	Since time.Time
	// RetryAfter becomes the Retry-After response header.
	RetryAfter time.Duration
	// Refresh becomes the Refresh response header.
	Refresh time.Duration
	// Redirect is an optional absolute-path redirect.
	Redirect string
	// Secret is an optional URL-safe bypass token. It is never rendered by
	// status output, errors, logs, or runtime events.
	Secret string
}

// MaintenanceSource loads an immutable maintenance snapshot. Implementations
// must honor context cancellation and publish snapshots atomically.
type MaintenanceSource interface {
	// LoadMaintenance returns the latest complete snapshot.
	LoadMaintenance(context.Context) (MaintenanceState, error)
}

// MaintenanceStore controls maintenance mode and also supplies runtime state.
// A shared database or cache adapter can implement this interface without
// creating a dependency from service to that backend.
type MaintenanceStore interface {
	MaintenanceSource
	// StoreMaintenance atomically publishes an enabled snapshot.
	StoreMaintenance(context.Context, MaintenanceState) error
	// ClearMaintenance atomically disables maintenance.
	ClearMaintenance(context.Context) error
}

// MaintenanceStoreOperations adapts caller-owned shared storage operations.
type MaintenanceStoreOperations struct {
	// Load reads the latest complete caller-owned snapshot.
	Load func(context.Context) (MaintenanceState, error)
	// Store atomically publishes an enabled caller-owned snapshot.
	Store func(context.Context, MaintenanceState) error
	// Clear atomically disables the caller-owned snapshot.
	Clear func(context.Context) error
}

type sharedMaintenanceStore struct{ operations MaintenanceStoreOperations }

// NewSharedMaintenanceStore validates and adapts a caller-owned multi-instance
// storage implementation.
func NewSharedMaintenanceStore(operations MaintenanceStoreOperations) (MaintenanceStore, error) {
	if operations.Load == nil || operations.Store == nil || operations.Clear == nil {
		return nil, &DefinitionError{
			Field: "MaintenanceStoreOperations", Reason: "requires load, store, and clear",
		}
	}

	return &sharedMaintenanceStore{operations: operations}, nil
}

func (store *sharedMaintenanceStore) LoadMaintenance(ctx context.Context) (MaintenanceState, error) {
	state, err := store.operations.Load(ctx)
	if err != nil {
		return MaintenanceState{}, err
	}
	if err := validateMaintenanceState(state); err != nil {
		return MaintenanceState{}, err
	}

	return state, nil
}

func (store *sharedMaintenanceStore) StoreMaintenance(
	ctx context.Context,
	state MaintenanceState,
) error {
	if err := validateEnabledMaintenanceState(state); err != nil {
		return err
	}

	return store.operations.Store(ctx, state)
}

func (store *sharedMaintenanceStore) ClearMaintenance(ctx context.Context) error {
	return store.operations.Clear(ctx)
}

// FileMaintenanceStore is an atomic file-backed single-host maintenance store.
// Multiple processes may share it only when the filesystem provides atomic
// rename and coherent reads for the configured path.
type FileMaintenanceStore struct {
	path string
	mu   sync.Mutex
}

// NewFileMaintenanceStore constructs an inert file store without touching the
// filesystem.
func NewFileMaintenanceStore(path string) (*FileMaintenanceStore, error) {
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) {
		return nil, &DefinitionError{
			Field: "maintenance file path", Reason: "must not be blank or contain NUL",
		}
	}

	return &FileMaintenanceStore{path: filepath.Clean(path)}, nil
}

type persistedMaintenanceState struct {
	Enabled           bool   `json:"enabled"`
	Since             string `json:"since"`
	RetryAfterSeconds int64  `json:"retry_after_seconds,omitempty"`
	RefreshSeconds    int64  `json:"refresh_seconds,omitempty"`
	Redirect          string `json:"redirect,omitempty"`
	Secret            string `json:"secret,omitempty"`
}

// LoadMaintenance reads and validates the complete file snapshot.
func (store *FileMaintenanceStore) LoadMaintenance(ctx context.Context) (MaintenanceState, error) {
	if err := context.Cause(ctx); err != nil {
		return MaintenanceState{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return MaintenanceState{}, nil
	}
	if err != nil {
		return MaintenanceState{}, err
	}
	if len(data) > maximumMaintenanceStateBytes {
		return MaintenanceState{}, errors.New("maintenance state exceeds size limit")
	}
	var persisted persistedMaintenanceState
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&persisted); err != nil {
		return MaintenanceState{}, errors.New("maintenance state is invalid")
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return MaintenanceState{}, err
	}
	since, err := time.Parse(time.RFC3339Nano, persisted.Since)
	if err != nil {
		return MaintenanceState{}, errors.New("maintenance state timestamp is invalid")
	}
	state := MaintenanceState{
		Enabled: persisted.Enabled, Since: since,
		RetryAfter: time.Duration(persisted.RetryAfterSeconds) * time.Second,
		Refresh:    time.Duration(persisted.RefreshSeconds) * time.Second,
		Redirect:   persisted.Redirect, Secret: persisted.Secret,
	}
	if err := validateMaintenanceState(state); err != nil {
		return MaintenanceState{}, err
	}
	return state, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("maintenance state has trailing data")
	}

	return nil
}

// StoreMaintenance atomically publishes one enabled file snapshot.
func (store *FileMaintenanceStore) StoreMaintenance(
	ctx context.Context,
	state MaintenanceState,
) error {
	if err := validateEnabledMaintenanceState(state); err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	persisted := persistedMaintenanceState{
		Enabled: true, Since: state.Since.UTC().Format(time.RFC3339Nano),
		RetryAfterSeconds: int64(state.RetryAfter / time.Second),
		RefreshSeconds:    int64(state.Refresh / time.Second),
		Redirect:          state.Redirect, Secret: state.Secret,
	}
	// persistedMaintenanceState contains only JSON-native scalar fields, so
	// encoding cannot fail.
	// #nosec G117 -- the explicit 0600 credential state is required to validate
	// bypass requests and is never rendered through diagnostics or status.
	data, _ := json.Marshal(persisted)
	data = append(data, '\n')
	store.mu.Lock()
	defer store.mu.Unlock()
	temporaryPath := fmt.Sprintf("%s.%s.tmp", store.path, generateMaintenanceSecret())
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := os.WriteFile(temporaryPath, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, store.path); err != nil {
		return err
	}
	committed = true

	return nil
}

// ClearMaintenance removes the file snapshot. An absent file is already clear.
func (store *FileMaintenanceStore) ClearMaintenance(ctx context.Context) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	err := os.Remove(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return err
}

// Maintenance configures optional runtime maintenance behavior. When Store is
// set, down, up, and status commands are reserved and every long-running role
// polls the store, withdraws readiness, and gates business HTTP.
type Maintenance struct {
	// Store supplies runtime snapshots and the built-in command operations.
	Store MaintenanceStore
	// RefreshInterval controls how often a running process refreshes Store.
	RefreshInterval time.Duration
	// OperationTimeout bounds store operations. Zero selects one second.
	OperationTimeout time.Duration
	// Response optionally renders a caller-owned maintenance body. The platform
	// writes status 503 and maintenance headers before invoking it.
	Response http.Handler
}

type maintenanceRuntime struct {
	config        Maintenance
	observability *runtimeObservability
	state         atomic.Pointer[MaintenanceState]
}

func newMaintenanceRuntime(
	config Maintenance,
	observability *runtimeObservability,
) *maintenanceRuntime {
	runtime := &maintenanceRuntime{config: config, observability: observability}
	runtime.state.Store(&MaintenanceState{})

	return runtime
}

func firstMaintenanceRuntime(values []*maintenanceRuntime) *maintenanceRuntime {
	if len(values) == 0 {
		return nil
	}

	return values[0]
}

func (runtime *maintenanceRuntime) component() Component {
	return Component{
		Name: "service-maintenance-state",
		Start: func(ctx context.Context) error {
			return runtime.refresh(ctx, true)
		},
	}
}

func (runtime *maintenanceRuntime) task() Task {
	return Task{Name: "service-maintenance-refresh", Run: runtime.run}
}

func (runtime *maintenanceRuntime) run(ctx context.Context) error {
	ticker := time.NewTicker(resolvedMaintenanceRefreshInterval(runtime.config.RefreshInterval))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ticker.C:
			_ = runtime.refresh(ctx, false)
		}
	}
}

func (runtime *maintenanceRuntime) refresh(ctx context.Context, required bool) error {
	operationContext, cancel := context.WithTimeout(
		ctx,
		resolvedMaintenanceOperationTimeout(runtime.config.OperationTimeout),
	)
	state, err := runtime.config.Store.LoadMaintenance(operationContext)
	cancel()
	if err != nil {
		runtime.observability.event(
			ctx, RuntimeEventMaintenance, RuntimeResultFailed, "store", 0, false,
		)
		if required {
			return &MaintenanceError{Operation: "load", Err: err}
		}

		return err
	}
	previous := runtime.state.Load()
	stateCopy := state
	runtime.state.Store(&stateCopy)
	if previous.Enabled != state.Enabled {
		result := RuntimeResultAvailable
		if state.Enabled {
			result = RuntimeResultUnavailable
		}
		runtime.observability.event(
			ctx, RuntimeEventMaintenance, result, "state", 0, true,
		)
	}

	return nil
}

func (runtime *maintenanceRuntime) enabled() bool {
	return runtime != nil && runtime.state.Load().Enabled
}

func (runtime *maintenanceRuntime) admission(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		state := *runtime.state.Load()
		if !state.Enabled || maintenanceBypassed(request, state.Secret) ||
			(state.Redirect != "" && request.URL.Path == state.Redirect) {
			next.ServeHTTP(writer, request)
			return
		}
		applyMaintenanceHeaders(writer.Header(), state)
		if state.Secret != "" && request.URL.Path == "/"+state.Secret &&
			(request.Method == http.MethodGet || request.Method == http.MethodHead) {
			http.SetCookie(writer, &http.Cookie{
				Name: maintenanceCookieName, Value: maintenanceCookieValue(state.Secret),
				Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
			})
			http.Redirect(writer, request, "/", http.StatusFound)
			return
		}
		if state.Redirect != "" {
			http.Redirect(writer, request, state.Redirect, http.StatusFound)
			return
		}
		if runtime.config.Response != nil {
			custom := &maintenanceResponseWriter{
				ResponseWriter: writer,
				head:           request.Method == http.MethodHead,
			}
			runtime.config.Response.ServeHTTP(custom, request)
			if !custom.wroteHeader {
				custom.WriteHeader(http.StatusServiceUnavailable)
			}
			return
		}
		writer.WriteHeader(http.StatusServiceUnavailable)
		if request.Method == http.MethodHead {
			return
		}
		_, _ = io.WriteString(writer, "service unavailable\n")
	})
}

type maintenanceResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
	head        bool
}

func (writer *maintenanceResponseWriter) WriteHeader(int) {
	if writer.wroteHeader {
		return
	}
	writer.wroteHeader = true
	writer.ResponseWriter.WriteHeader(http.StatusServiceUnavailable)
}

func (writer *maintenanceResponseWriter) Write(body []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}
	if writer.head {
		return len(body), nil
	}

	return writer.ResponseWriter.Write(body)
}

func (writer *maintenanceResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func applyMaintenanceHeaders(header http.Header, state MaintenanceState) {
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Type", "text/plain; charset=utf-8")
	header.Set("X-Content-Type-Options", "nosniff")
	if state.RetryAfter > 0 {
		header.Set("Retry-After", fmt.Sprintf("%d", int64(state.RetryAfter/time.Second)))
	}
	if state.Refresh > 0 {
		header.Set("Refresh", fmt.Sprintf("%d", int64(state.Refresh/time.Second)))
	}
}

func maintenanceBypassed(request *http.Request, secret string) bool {
	if secret == "" {
		return false
	}
	cookie, err := request.Cookie(maintenanceCookieName)
	if err != nil {
		return false
	}
	expected := maintenanceCookieValue(secret)
	return len(cookie.Value) == len(expected) &&
		subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(expected)) == 1
}

func maintenanceCookieValue(secret string) string {
	digest := sha256.Sum256([]byte("go-service/maintenance-bypass\x00" + secret))

	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func validateMaintenance(config Maintenance) error {
	if config.Store == nil {
		if config.RefreshInterval != 0 || config.OperationTimeout != 0 || config.Response != nil {
			return &DefinitionError{
				Field: "Maintenance.Store", Reason: "is required when maintenance is configured",
			}
		}

		return nil
	}
	if config.RefreshInterval < 0 {
		return &DefinitionError{Field: "Maintenance.RefreshInterval", Reason: "must not be negative"}
	}
	if config.OperationTimeout < 0 {
		return &DefinitionError{Field: "Maintenance.OperationTimeout", Reason: "must not be negative"}
	}
	if resolvedMaintenanceRefreshInterval(config.RefreshInterval) < 10*time.Millisecond {
		return &DefinitionError{
			Field: "Maintenance.RefreshInterval", Reason: "must be at least 10ms",
		}
	}
	if resolvedMaintenanceOperationTimeout(config.OperationTimeout) > time.Minute {
		return &DefinitionError{
			Field: "Maintenance.OperationTimeout", Reason: "must not exceed one minute",
		}
	}

	return nil
}

func resolvedMaintenanceRefreshInterval(value time.Duration) time.Duration {
	if value == 0 {
		return defaultMaintenanceRefreshInterval
	}

	return value
}

func resolvedMaintenanceOperationTimeout(value time.Duration) time.Duration {
	if value == 0 {
		return defaultMaintenanceOperationTimeout
	}

	return value
}

func validateEnabledMaintenanceState(state MaintenanceState) error {
	if !state.Enabled {
		return errors.New("maintenance state must be enabled")
	}
	if state.Since.IsZero() {
		return errors.New("maintenance state requires a timestamp")
	}

	return validateMaintenanceState(state)
}

func validateMaintenanceState(state MaintenanceState) error {
	if state.RetryAfter < 0 || state.RetryAfter > maximumMaintenanceDuration ||
		state.Refresh < 0 || state.Refresh > maximumMaintenanceDuration {
		return errors.New("maintenance duration is invalid")
	}
	if state.Redirect != "" && (!strings.HasPrefix(state.Redirect, "/") ||
		strings.HasPrefix(state.Redirect, "//") || strings.ContainsAny(state.Redirect, "\r\n")) {
		return errors.New("maintenance redirect is invalid")
	}
	if state.Secret != "" && !validMaintenanceSecret(state.Secret) {
		return errors.New("maintenance secret is invalid")
	}

	return nil
}

func validMaintenanceSecret(secret string) bool {
	if len(secret) < 8 || len(secret) > 128 {
		return false
	}
	for index := range len(secret) {
		character := secret[index]
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' {
			return false
		}
	}

	return true
}

func generateMaintenanceSecret() string {
	return rand.Text()
}

type maintenanceStatus struct {
	Enabled           bool   `json:"enabled"`
	Since             string `json:"since,omitempty"`
	RetryAfterSeconds int64  `json:"retry_after_seconds,omitempty"`
	RefreshSeconds    int64  `json:"refresh_seconds,omitempty"`
	Redirect          string `json:"redirect,omitempty"`
	BypassConfigured  bool   `json:"bypass_configured"`
}

func maintenanceCommandSpecs(
	definition Definition,
	invocation Invocation,
	factory *correlation.Factory,
	execution *executionState,
) []cli.CommandSpec {
	if definition.Maintenance.Store == nil {
		return nil
	}
	retry := cli.DurationOption("retry").Description("Retry-After duration")
	refresh := cli.DurationOption("refresh").Description("browser refresh duration")
	secret := cli.StringOption("secret").Secret().Description("maintenance bypass token")
	withSecret := cli.BoolOption("with-secret").Description("generate a bypass token")
	redirect := cli.StringOption("redirect").Description("absolute-path redirect")
	return []cli.CommandSpec{
		{
			Name: "down", Summary: "enable maintenance mode",
			Options: []cli.OptionDefinition{retry, refresh, secret, withSecret, redirect},
			Handler: maintenanceCommandHandler(
				"down", definition, invocation, factory, execution,
				func(ctx context.Context, commandInvocation cli.Invocation) error {
					state := MaintenanceState{
						Enabled: true, Since: time.Now(), RetryAfter: retry.Get(commandInvocation.Input()),
						Refresh: refresh.Get(commandInvocation.Input()),
						Secret:  secret.Get(commandInvocation.Input()), Redirect: redirect.Get(commandInvocation.Input()),
					}
					if withSecret.Get(commandInvocation.Input()) {
						if state.Secret != "" {
							return &DefinitionError{
								Field: "down options", Reason: "secret and with-secret are mutually exclusive",
							}
						}
						state.Secret = generateMaintenanceSecret()
					}
					if err := maintenanceStoreState(ctx, definition.Maintenance, state); err != nil {
						return err
					}
					if _, err := io.WriteString(invocation.Stdout, "maintenance enabled\n"); err != nil {
						return err
					}
					if withSecret.Get(commandInvocation.Input()) {
						_, err := fmt.Fprintf(invocation.Stdout, "maintenance secret: %s\n", state.Secret)
						return err
					}

					return nil
				},
			),
		},
		{
			Name: "up", Summary: "disable maintenance mode",
			Handler: maintenanceCommandHandler(
				"up", definition, invocation, factory, execution,
				func(ctx context.Context, _ cli.Invocation) error {
					operationContext, cancel := context.WithTimeout(
						ctx, resolvedMaintenanceOperationTimeout(definition.Maintenance.OperationTimeout),
					)
					err := definition.Maintenance.Store.ClearMaintenance(operationContext)
					cancel()
					if err != nil {
						return &MaintenanceError{Operation: "disable", Err: err}
					}
					_, err = io.WriteString(invocation.Stdout, "maintenance disabled\n")

					return err
				},
			),
		},
		{
			Name: "status", Summary: "show maintenance status",
			Handler: maintenanceCommandHandler(
				"status", definition, invocation, factory, execution,
				func(ctx context.Context, _ cli.Invocation) error {
					operationContext, cancel := context.WithTimeout(
						ctx, resolvedMaintenanceOperationTimeout(definition.Maintenance.OperationTimeout),
					)
					state, err := definition.Maintenance.Store.LoadMaintenance(operationContext)
					cancel()
					if err != nil {
						return &MaintenanceError{Operation: "status", Err: err}
					}
					status := maintenanceStatus{Enabled: state.Enabled, BypassConfigured: state.Secret != ""}
					if state.Enabled {
						status.Since = state.Since.UTC().Format(time.RFC3339Nano)
						status.RetryAfterSeconds = int64(state.RetryAfter / time.Second)
						status.RefreshSeconds = int64(state.Refresh / time.Second)
						status.Redirect = state.Redirect
					}

					return json.NewEncoder(invocation.Stdout).Encode(status)
				},
			),
		},
	}
}

func maintenanceCommandHandler(
	role string,
	definition Definition,
	invocation Invocation,
	factory *correlation.Factory,
	execution *executionState,
	operation func(context.Context, cli.Invocation) error,
) cli.Handler {
	return func(ctx context.Context, commandInvocation cli.Invocation) error {
		execution.selected = CommandKindOneShot
		coordinated := coordinateCommandSignals(ctx, invocation.Signals)
		defer coordinated.stop()
		values, err := factory.Start()
		if err != nil {
			return &ConstructionError{Command: role, Err: err}
		}
		runtimeContext := correlation.WithValues(coordinated.context, values)
		observability := newRuntimeObservability(
			runtimeContext,
			ProcessIdentity{Identity: definition.Identity, Role: role},
			definition.Logger,
			definition.Observer,
			definition.CorrelationDisclosure,
		)
		err = operation(runtimeContext, commandInvocation)
		result := RuntimeResultSucceeded
		transition := false
		boundary := "status"
		switch role {
		case "down":
			result = RuntimeResultUnavailable
			transition = true
			boundary = "state"
		case "up":
			result = RuntimeResultAvailable
			transition = true
			boundary = "state"
		}
		if err != nil {
			result = RuntimeResultFailed
		}
		observability.event(
			runtimeContext, RuntimeEventMaintenance, result, boundary, 0, transition,
		)

		return preserveCommandSignal(runtimeContext, err)
	}
}

func maintenanceStoreState(
	ctx context.Context,
	config Maintenance,
	state MaintenanceState,
) error {
	if err := validateEnabledMaintenanceState(state); err != nil {
		return &DefinitionError{Field: "down options", Reason: err.Error()}
	}
	operationContext, cancel := context.WithTimeout(
		ctx, resolvedMaintenanceOperationTimeout(config.OperationTimeout),
	)
	err := config.Store.StoreMaintenance(operationContext, state)
	cancel()
	if err != nil {
		return &MaintenanceError{Operation: "enable", Err: err}
	}

	return nil
}
