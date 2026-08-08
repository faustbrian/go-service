package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/cli"
	"github.com/faustbrian/golib/pkg/correlation"
)

func TestMaintenanceErrorAndStoreAdaptersPreserveSafeContracts(t *testing.T) {
	cause := errors.New("backend unavailable: secret-value")
	failure := &MaintenanceError{Operation: "load", Err: cause}
	if failure.Error() != "maintenance load failed: service maintenance failure" ||
		!errors.Is(failure, ErrMaintenance) || !errors.Is(failure, cause) ||
		strings.Contains(failure.Error(), "secret-value") {
		t.Fatalf("maintenance error contract = %q", failure)
	}
	if code := exitCode(failure); code != exitTemporary {
		t.Fatalf("maintenance exit code = %d, want %d", code, exitTemporary)
	}
	unsafeOperation := &MaintenanceError{Operation: "secret-value", Err: cause}
	if strings.Contains(unsafeOperation.Error(), "secret-value") {
		t.Fatalf("maintenance operation leaked through error = %q", unsafeOperation)
	}

	valid := MaintenanceState{Enabled: true, Since: time.Now(), RetryAfter: time.Minute}
	for name, operations := range map[string]MaintenanceStoreOperations{
		"load":  {Store: func(context.Context, MaintenanceState) error { return nil }, Clear: func(context.Context) error { return nil }},
		"store": {Load: func(context.Context) (MaintenanceState, error) { return MaintenanceState{}, nil }, Clear: func(context.Context) error { return nil }},
		"clear": {Load: func(context.Context) (MaintenanceState, error) { return MaintenanceState{}, nil }, Store: func(context.Context, MaintenanceState) error { return nil }},
	} {
		t.Run("missing-"+name, func(t *testing.T) {
			if _, err := NewSharedMaintenanceStore(operations); !errors.Is(err, ErrInvalidDefinition) {
				t.Fatalf("NewSharedMaintenanceStore() error = %v", err)
			}
		})
	}

	var stored MaintenanceState
	cleared := false
	shared, err := NewSharedMaintenanceStore(MaintenanceStoreOperations{
		Load:  func(context.Context) (MaintenanceState, error) { return stored, nil },
		Store: func(_ context.Context, state MaintenanceState) error { stored = state; return nil },
		Clear: func(context.Context) error { cleared = true; return nil },
	})
	if err != nil {
		t.Fatalf("NewSharedMaintenanceStore() error = %v", err)
	}
	if err := shared.StoreMaintenance(t.Context(), valid); err != nil {
		t.Fatalf("StoreMaintenance() error = %v", err)
	}
	if loaded, err := shared.LoadMaintenance(t.Context()); err != nil || loaded.RetryAfter != time.Minute {
		t.Fatalf("LoadMaintenance() = %#v, %v", loaded, err)
	}
	if err := shared.ClearMaintenance(t.Context()); err != nil || !cleared {
		t.Fatalf("ClearMaintenance() = %v, cleared = %v", err, cleared)
	}
	if err := shared.StoreMaintenance(t.Context(), MaintenanceState{}); err == nil {
		t.Fatal("StoreMaintenance() accepted disabled state")
	}

	backendErr := errors.New("backend error")
	failing, err := NewSharedMaintenanceStore(MaintenanceStoreOperations{
		Load:  func(context.Context) (MaintenanceState, error) { return MaintenanceState{}, backendErr },
		Store: func(context.Context, MaintenanceState) error { return backendErr },
		Clear: func(context.Context) error { return backendErr },
	})
	if err != nil {
		t.Fatalf("NewSharedMaintenanceStore() error = %v", err)
	}
	if _, err := failing.LoadMaintenance(t.Context()); !errors.Is(err, backendErr) {
		t.Fatalf("LoadMaintenance() error = %v", err)
	}
	if err := failing.StoreMaintenance(t.Context(), valid); !errors.Is(err, backendErr) {
		t.Fatalf("StoreMaintenance() error = %v", err)
	}
	if err := failing.ClearMaintenance(t.Context()); !errors.Is(err, backendErr) {
		t.Fatalf("ClearMaintenance() error = %v", err)
	}

	invalidLoad, err := NewSharedMaintenanceStore(MaintenanceStoreOperations{
		Load: func(context.Context) (MaintenanceState, error) {
			return MaintenanceState{RetryAfter: -time.Second}, nil
		},
		Store: func(context.Context, MaintenanceState) error { return nil },
		Clear: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewSharedMaintenanceStore() error = %v", err)
	}
	if _, err := invalidLoad.LoadMaintenance(t.Context()); err == nil {
		t.Fatal("LoadMaintenance() accepted invalid state")
	}
}

type commandMaintenanceStore struct {
	state    MaintenanceState
	loadErr  error
	storeErr error
	clearErr error
}

func (store *commandMaintenanceStore) LoadMaintenance(context.Context) (MaintenanceState, error) {
	return store.state, store.loadErr
}

func (store *commandMaintenanceStore) StoreMaintenance(_ context.Context, state MaintenanceState) error {
	store.state = state
	return store.storeErr
}

func (store *commandMaintenanceStore) ClearMaintenance(context.Context) error {
	store.state = MaintenanceState{}
	return store.clearErr
}

func maintenanceCommandDefinition(store MaintenanceStore) Definition {
	migrate := CommandFor(CommandSpec[struct{}]{
		Name: "migrate", Kind: CommandKindOneShot,
		Load:  func(context.Context, Invocation) (struct{}, error) { return struct{}{}, nil },
		Build: func(context.Context, BuildContext, struct{}) (Plan, error) { return Plan{}, nil },
	})

	return Definition{
		Identity:    Identity{Name: "postal"},
		Commands:    Commands{Migrate: migrate},
		Maintenance: Maintenance{Store: store},
	}
}

func executeMaintenanceForTest(
	definition Definition,
	args []string,
	stdout io.Writer,
) int {
	return Execute(context.Background(), definition, Invocation{
		Args: args, Stdout: stdout, Stderr: io.Discard,
	})
}

type failAfterWriter struct {
	writes int
	failAt int
	buffer bytes.Buffer
}

func (writer *failAfterWriter) Write(data []byte) (int, error) {
	writer.writes++
	if writer.writes >= writer.failAt {
		return 0, errors.New("write failed")
	}

	return writer.buffer.Write(data)
}

func TestMaintenanceCommandsCoverSuccessFailureAndSecretSafety(t *testing.T) {
	store := &commandMaintenanceStore{}
	definition := maintenanceCommandDefinition(store)
	if exit := executeMaintenanceForTest(
		definition,
		[]string{"down", "--secret=valid-token", "--with-secret"},
		io.Discard,
	); exit == 0 {
		t.Fatal("down accepted both secret options")
	}
	if exit := executeMaintenanceForTest(
		definition,
		[]string{"down", "--retry=-1s"},
		io.Discard,
	); exit == 0 {
		t.Fatal("down accepted a negative retry")
	}

	var generated bytes.Buffer
	if exit := executeMaintenanceForTest(
		definition,
		[]string{"down", "--with-secret"},
		&generated,
	); exit != 0 || !store.state.Enabled || !validMaintenanceSecret(store.state.Secret) ||
		!strings.Contains(generated.String(), "maintenance secret: ") {
		t.Fatalf("generated down = exit %d, state %#v, output %q", exit, store.state, generated.String())
	}
	store.state.RetryAfter = time.Minute
	store.state.Refresh = 15 * time.Second
	store.state.Redirect = "/maintenance"
	var enabledStatus bytes.Buffer
	if exit := executeMaintenanceForTest(
		definition,
		[]string{"status"},
		&enabledStatus,
	); exit != 0 || !strings.Contains(enabledStatus.String(), `"retry_after_seconds":60`) ||
		!strings.Contains(enabledStatus.String(), `"refresh_seconds":15`) ||
		!strings.Contains(enabledStatus.String(), `"redirect":"/maintenance"`) {
		t.Fatalf("enabled status = exit %d, output %q", exit, enabledStatus.String())
	}
	var disabledStatus bytes.Buffer
	store.state = MaintenanceState{}
	if exit := executeMaintenanceForTest(
		definition,
		[]string{"status"},
		&disabledStatus,
	); exit != 0 || disabledStatus.String() != "{\"enabled\":false,\"bypass_configured\":false}\n" {
		t.Fatalf("disabled status = exit %d, output %q", exit, disabledStatus.String())
	}

	store.storeErr = errors.New("store failure")
	if exit := executeMaintenanceForTest(definition, []string{"down"}, io.Discard); exit == 0 {
		t.Fatal("down hid a store failure")
	}
	store.storeErr = nil
	if exit := executeMaintenanceForTest(
		definition,
		[]string{"down"},
		&failAfterWriter{failAt: 1},
	); exit == 0 {
		t.Fatal("down hid its output failure")
	}
	if exit := executeMaintenanceForTest(
		definition,
		[]string{"down", "--with-secret"},
		&failAfterWriter{failAt: 2},
	); exit == 0 {
		t.Fatal("down hid generated-secret output failure")
	}

	store.clearErr = errors.New("clear failure")
	if exit := executeMaintenanceForTest(definition, []string{"up"}, io.Discard); exit == 0 {
		t.Fatal("up hid a clear failure")
	}
	store.clearErr = nil
	if exit := executeMaintenanceForTest(
		definition,
		[]string{"up"},
		&failAfterWriter{failAt: 1},
	); exit == 0 {
		t.Fatal("up hid its output failure")
	}

	store.loadErr = errors.New("load failure")
	if exit := executeMaintenanceForTest(definition, []string{"status"}, io.Discard); exit == 0 {
		t.Fatal("status hid a load failure")
	}
	store.loadErr = nil
	if exit := executeMaintenanceForTest(
		definition,
		[]string{"status"},
		&failAfterWriter{failAt: 1},
	); exit == 0 {
		t.Fatal("status hid its output failure")
	}
}

func TestMaintenanceCommandsUsePlatformIdentityCorrelationAndEvents(t *testing.T) {
	store := &commandMaintenanceStore{}
	definition := maintenanceCommandDefinition(store)
	var output bytes.Buffer
	var events []RuntimeEvent
	definition.Logger = slog.New(slog.NewJSONHandler(&output, nil))
	definition.Observer = RuntimeObserverFunc(func(_ context.Context, event RuntimeEvent) {
		events = append(events, event)
	})
	definition.CorrelationDisclosure = correlation.DisclosurePolicy{Mode: correlation.ExposeDisclosure}
	if exit := executeMaintenanceForTest(definition, []string{"down"}, io.Discard); exit != 0 {
		t.Fatalf("down exit = %d", exit)
	}
	if exit := executeMaintenanceForTest(definition, []string{"status"}, io.Discard); exit != 0 {
		t.Fatalf("status exit = %d", exit)
	}
	if exit := executeMaintenanceForTest(definition, []string{"up"}, io.Discard); exit != 0 {
		t.Fatalf("up exit = %d", exit)
	}
	want := map[string]RuntimeEventResult{
		"down":   RuntimeResultUnavailable,
		"status": RuntimeResultSucceeded,
		"up":     RuntimeResultAvailable,
	}
	for _, event := range events {
		if event.Kind == RuntimeEventMaintenance && want[event.Identity.Role] == event.Result {
			delete(want, event.Identity.Role)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing maintenance command events: %#v in %#v", want, events)
	}
	for _, role := range []string{"down", "status", "up"} {
		if !strings.Contains(output.String(), `"process.role":"`+role+`"`) {
			t.Fatalf("logs lack %s role: %q", role, output.String())
		}
	}
	if !strings.Contains(output.String(), `"correlation.id":`) ||
		!strings.Contains(output.String(), `"request.id":`) {
		t.Fatalf("maintenance logs lack correlation identity: %q", output.String())
	}
}

func TestMaintenanceCommandReportsCorrelationConstructionFailure(t *testing.T) {
	wantErr := errors.New("entropy unavailable")
	factory, err := correlation.NewFactory(correlation.FactoryOptions{
		Generator: correlation.GeneratorFunc(func() (string, error) { return "", wantErr }),
	})
	if err != nil {
		t.Fatalf("NewFactory() error = %v", err)
	}
	execution := &executionState{}
	handler := maintenanceCommandHandler(
		"status",
		maintenanceCommandDefinition(&commandMaintenanceStore{}),
		Invocation{},
		factory,
		execution,
		func(context.Context, cli.Invocation) error {
			t.Fatal("operation ran after correlation failure")
			return nil
		},
	)
	err = handler(t.Context(), cli.Invocation{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("handler error = %v", err)
	}
	if execution.selected != CommandKindOneShot {
		t.Fatalf("selected kind = %v", execution.selected)
	}
}

func TestMaintenanceDefinitionRejectsInvalidConfigurationAndReservedCommands(t *testing.T) {
	store := &commandMaintenanceStore{}
	definition := maintenanceCommandDefinition(store)
	definition.Maintenance.RefreshInterval = -time.Second
	if exit := executeMaintenanceForTest(definition, []string{"migrate"}, io.Discard); exit == 0 {
		t.Fatal("Execute() accepted invalid maintenance configuration")
	}
	definition = maintenanceCommandDefinition(store)
	definition.CorrelationDisclosure = correlation.DisclosurePolicy{Mode: 99}
	if exit := executeMaintenanceForTest(definition, []string{"migrate"}, io.Discard); exit == 0 {
		t.Fatal("Execute() accepted invalid correlation disclosure")
	}
	commandNamed := func(name string) Command {
		return CommandFor(CommandSpec[struct{}]{
			Name: name, Kind: CommandKindOneShot,
			Load:  func(context.Context, Invocation) (struct{}, error) { return struct{}{}, nil },
			Build: func(context.Context, BuildContext, struct{}) (Plan, error) { return Plan{}, nil },
		})
	}
	for _, name := range []string{"down", "up", "status"} {
		definition = maintenanceCommandDefinition(store)
		definition.Commands.Custom = []Command{commandNamed(name)}
		_, _, err := compileDefinition(definition, Invocation{Stdout: io.Discard, Stderr: io.Discard})
		var definitionErr *DefinitionError
		if !errors.As(err, &definitionErr) || definitionErr.Field != "Commands[1].Name" ||
			definitionErr.Reason != "is reserved by maintenance mode" {
			t.Fatalf("%s collision error = %#v", name, err)
		}
	}
	definition = maintenanceCommandDefinition(store)
	definition.Commands.Custom = []Command{commandNamed("repair")}
	if exit := executeMaintenanceForTest(definition, []string{"repair"}, io.Discard); exit != 0 {
		t.Fatalf("Execute() rejected non-reserved custom command: %d", exit)
	}
	definition.Maintenance = Maintenance{}
	definition.Commands.Custom = []Command{commandNamed("down")}
	if exit := executeMaintenanceForTest(definition, []string{"down"}, io.Discard); exit != 0 {
		t.Fatalf("Execute() reserved down without maintenance: %d", exit)
	}
}

func TestMaintenanceRuntimeAppliesOnlyToLongRunningCommands(t *testing.T) {
	store := &commandMaintenanceStore{}
	oneShot := maintenanceCommandDefinition(store)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if exit := Execute(ctx, oneShot, Invocation{
		Args: []string{"migrate"}, Stdout: io.Discard, Stderr: io.Discard,
	}); exit != 0 {
		t.Fatalf("one-shot maintenance definition exit = %d", exit)
	}

	store.loadErr = errors.New("store unavailable")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	worker := CommandFor(CommandSpec[struct{}]{
		Name: "worker", Kind: CommandKindLongRunning,
		Load: func(context.Context, Invocation) (struct{}, error) { return struct{}{}, nil },
		Build: func(context.Context, BuildContext, struct{}) (Plan, error) {
			return Plan{Tasks: []Task{{
				Name: "worker-loop",
				Run: func(ctx context.Context) error {
					<-ctx.Done()
					return context.Cause(ctx)
				},
			}}}, nil
		},
	})
	longRunning := Definition{
		Identity: Identity{Name: "postal"}, Commands: Commands{Worker: worker},
		Management: Management{Listener: listener}, Maintenance: Maintenance{Store: store},
	}
	ctx, cancel = context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if exit := Execute(ctx, longRunning, Invocation{
		Args: []string{"worker"}, Stdout: io.Discard, Stderr: io.Discard,
	}); exit != exitTemporary {
		t.Fatalf("required maintenance load exit = %d, want %d", exit, exitTemporary)
	}
}

func TestFileMaintenanceStoreRoundTripsAndRejectsInvalidArtifacts(t *testing.T) {
	if _, err := NewFileMaintenanceStore(""); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("empty path error = %v", err)
	}
	if _, err := NewFileMaintenanceStore("bad\x00path"); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("NUL path error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "maintenance.json")
	store, err := NewFileMaintenanceStore(path)
	if err != nil {
		t.Fatalf("NewFileMaintenanceStore() error = %v", err)
	}
	if state, err := store.LoadMaintenance(t.Context()); err != nil || state.Enabled {
		t.Fatalf("initial LoadMaintenance() = %#v, %v", state, err)
	}
	legacyTemporary := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(legacyTemporary, []byte("unrelated"), 0o600); err != nil {
		t.Fatalf("write unrelated temporary file: %v", err)
	}
	exactState := []byte(`{"enabled":true,"since":"2026-08-08T00:00:00Z"}`)
	exactState = append(exactState, bytes.Repeat([]byte(" "), maximumMaintenanceStateBytes-len(exactState))...)
	if err := os.WriteFile(path, exactState, 0o600); err != nil {
		t.Fatalf("write exact-size maintenance state: %v", err)
	}
	if loaded, err := store.LoadMaintenance(t.Context()); err != nil || !loaded.Enabled {
		t.Fatalf("exact-size LoadMaintenance() = %#v, %v", loaded, err)
	}
	state := MaintenanceState{
		Enabled: true, Since: time.Now().UTC().Round(0), RetryAfter: time.Minute,
		Refresh: 15 * time.Second, Redirect: "/maintenance", Secret: "valid-token",
	}
	if err := store.StoreMaintenance(t.Context(), state); err != nil {
		t.Fatalf("StoreMaintenance() error = %v", err)
	}
	if data, err := os.ReadFile(legacyTemporary); err != nil || string(data) != "unrelated" {
		t.Fatalf("unrelated temporary file = %q, %v", data, err)
	}
	loaded, err := store.LoadMaintenance(t.Context())
	if err != nil || loaded != state {
		t.Fatalf("LoadMaintenance() = %#v, %v; want %#v", loaded, err, state)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("maintenance file mode = %o, want 600", info.Mode().Perm())
	}
	if err := store.ClearMaintenance(t.Context()); err != nil {
		t.Fatalf("ClearMaintenance() error = %v", err)
	}
	if err := store.ClearMaintenance(t.Context()); err != nil {
		t.Fatalf("repeated ClearMaintenance() error = %v", err)
	}
	if err := store.StoreMaintenance(t.Context(), MaintenanceState{}); err == nil {
		t.Fatal("StoreMaintenance() accepted disabled state")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.LoadMaintenance(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled LoadMaintenance() error = %v", err)
	}
	if err := store.StoreMaintenance(canceled, state); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled StoreMaintenance() error = %v", err)
	}
	if err := store.ClearMaintenance(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ClearMaintenance() error = %v", err)
	}

	fixtures := map[string]string{
		"invalid-json":      "{",
		"trailing-json":     `{"enabled":true,"since":"2026-08-08T00:00:00Z"} {}`,
		"unknown-field":     `{"enabled":true,"since":"2026-08-08T00:00:00Z","unknown":true}`,
		"invalid-timestamp": `{"enabled":true,"since":"yesterday"}`,
		"invalid-duration":  `{"enabled":true,"since":"2026-08-08T00:00:00Z","retry_after_seconds":9999999}`,
		"invalid-redirect":  `{"enabled":true,"since":"2026-08-08T00:00:00Z","redirect":"//host"}`,
		"invalid-secret":    `{"enabled":true,"since":"2026-08-08T00:00:00Z","secret":"bad?token"}`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			if _, err := store.LoadMaintenance(t.Context()); err == nil {
				t.Fatal("LoadMaintenance() accepted invalid artifact")
			}
		})
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, maximumMaintenanceStateBytes+1), 0o600); err != nil {
		t.Fatalf("write oversized fixture: %v", err)
	}
	if _, err := store.LoadMaintenance(t.Context()); err == nil {
		t.Fatal("LoadMaintenance() accepted oversized artifact")
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove fixture: %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if _, err := store.LoadMaintenance(t.Context()); err == nil {
		t.Fatal("LoadMaintenance() read a directory")
	}
	if err := store.StoreMaintenance(t.Context(), state); err == nil {
		t.Fatal("StoreMaintenance() replaced a directory")
	}
	if err := store.ClearMaintenance(t.Context()); err != nil {
		t.Fatalf("ClearMaintenance(directory) error = %v", err)
	}

	missingStore, err := NewFileMaintenanceStore(filepath.Join(path, "missing", "state.json"))
	if err != nil {
		t.Fatalf("NewFileMaintenanceStore() error = %v", err)
	}
	if err := missingStore.StoreMaintenance(t.Context(), state); err == nil {
		t.Fatal("StoreMaintenance() wrote through a missing directory")
	}
}

func TestMaintenanceValidationAndAdmissionBoundaries(t *testing.T) {
	store, err := NewSharedMaintenanceStore(MaintenanceStoreOperations{
		Load:  func(context.Context) (MaintenanceState, error) { return MaintenanceState{}, nil },
		Store: func(context.Context, MaintenanceState) error { return nil },
		Clear: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewSharedMaintenanceStore() error = %v", err)
	}
	for name, config := range map[string]Maintenance{
		"missing-store":    {RefreshInterval: time.Second},
		"negative-refresh": {Store: store, RefreshInterval: -time.Second},
		"short-refresh":    {Store: store, RefreshInterval: time.Millisecond},
		"negative-timeout": {Store: store, OperationTimeout: -time.Second},
		"long-timeout":     {Store: store, OperationTimeout: time.Minute + time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateMaintenance(config); !errors.Is(err, ErrInvalidDefinition) {
				t.Fatalf("validateMaintenance() error = %v", err)
			}
		})
	}
	if err := validateMaintenance(Maintenance{}); err != nil {
		t.Fatalf("zero maintenance error = %v", err)
	}
	if err := validateMaintenance(Maintenance{
		Store: store, RefreshInterval: 10 * time.Millisecond, OperationTimeout: time.Minute,
	}); err != nil {
		t.Fatalf("exact maintenance bounds error = %v", err)
	}
	if resolvedMaintenanceRefreshInterval(0) != time.Second ||
		resolvedMaintenanceRefreshInterval(time.Minute) != time.Minute ||
		resolvedMaintenanceOperationTimeout(0) != time.Second {
		t.Fatal("maintenance defaults are inconsistent")
	}

	states := []MaintenanceState{
		{Enabled: true},
		{Enabled: true, Since: time.Now(), RetryAfter: -time.Second},
		{Enabled: true, Since: time.Now(), Refresh: maximumMaintenanceDuration + time.Second},
		{Enabled: true, Since: time.Now(), Redirect: "relative"},
		{Enabled: true, Since: time.Now(), Redirect: "/bad\nheader"},
		{Enabled: true, Since: time.Now(), Secret: "short"},
		{Enabled: true, Since: time.Now(), Secret: "invalid_token"},
	}
	for _, state := range states {
		if err := validateEnabledMaintenanceState(state); err == nil {
			t.Fatalf("validateEnabledMaintenanceState(%#v) accepted invalid state", state)
		}
	}
	if secret := generateMaintenanceSecret(); !validMaintenanceSecret(secret) {
		t.Fatalf("generated invalid maintenance secret %q", secret)
	}
	for _, secret := range []string{
		"aaaaaaaa", strings.Repeat("z", 128), "aAzZ09--",
	} {
		if !validMaintenanceSecret(secret) {
			t.Fatalf("validMaintenanceSecret(%q) = false", secret)
		}
	}
	for _, state := range []MaintenanceState{
		{Enabled: true, Since: time.Now(), RetryAfter: maximumMaintenanceDuration},
		{Enabled: true, Since: time.Now(), Refresh: maximumMaintenanceDuration},
	} {
		if err := validateEnabledMaintenanceState(state); err != nil {
			t.Fatalf("exact duration bound error = %v", err)
		}
	}

	runtime := newMaintenanceRuntime(Maintenance{}, nil)
	nextCalled := false
	handler := runtime.admission(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !nextCalled {
		t.Fatal("disabled maintenance did not admit request")
	}

	runtime.state.Store(&MaintenanceState{Enabled: true, Since: time.Now()})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, "/", nil))
	if recorder.Code != http.StatusServiceUnavailable || recorder.Body.Len() != 0 {
		t.Fatalf("default HEAD response = %d %q", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusServiceUnavailable || recorder.Body.String() != "service unavailable\n" {
		t.Fatalf("default response = %d %q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Retry-After") != "" || recorder.Header().Get("Refresh") != "" {
		t.Fatalf("zero maintenance headers = %#v", recorder.Header())
	}

	runtime.state.Store(&MaintenanceState{
		Enabled: true, Since: time.Now(), Secret: "valid-token",
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{
		Name:  maintenanceCookieName,
		Value: strings.Repeat("x", len(maintenanceCookieValue("valid-token"))),
	})
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("invalid bypass cookie status = %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/valid-token", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("non-GET bypass status = %d", recorder.Code)
	}
}

type mutableMaintenanceStore struct {
	mu    sync.Mutex
	state MaintenanceState
	err   error
}

func (store *mutableMaintenanceStore) LoadMaintenance(context.Context) (MaintenanceState, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.state, store.err
}

func (*mutableMaintenanceStore) StoreMaintenance(context.Context, MaintenanceState) error { return nil }
func (*mutableMaintenanceStore) ClearMaintenance(context.Context) error                   { return nil }

func TestMaintenanceRefreshRetainsLastValidStateAfterRuntimeFailure(t *testing.T) {
	store := &mutableMaintenanceStore{state: MaintenanceState{Enabled: true, Since: time.Now()}}
	var events []RuntimeEvent
	observability := newRuntimeObservability(
		t.Context(), ProcessIdentity{Identity: Identity{Name: "postal"}, Role: "serve"},
		nil, RuntimeObserverFunc(func(_ context.Context, event RuntimeEvent) {
			events = append(events, event)
		}), correlationDisclosureForTest(),
	)
	runtime := newMaintenanceRuntime(Maintenance{Store: store}, observability)
	if err := runtime.refresh(t.Context(), true); err != nil || !runtime.enabled() {
		t.Fatalf("initial refresh = %v, enabled = %v", err, runtime.enabled())
	}
	if len(events) != 1 || events[0].Result != RuntimeResultUnavailable || !events[0].Transition {
		t.Fatalf("initial maintenance transition = %#v", events)
	}
	if err := runtime.refresh(t.Context(), false); err != nil || len(events) != 1 {
		t.Fatalf("unchanged refresh = %v, events = %#v", err, events)
	}
	store.mu.Lock()
	store.err = errors.New("temporary backend failure")
	store.mu.Unlock()
	if err := runtime.refresh(t.Context(), false); err == nil || !runtime.enabled() {
		t.Fatalf("optional refresh = %v, enabled = %v", err, runtime.enabled())
	}
	if err := runtime.refresh(t.Context(), true); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("required refresh error = %v", err)
	}
	failures := 0
	for _, event := range events {
		if event.Kind == RuntimeEventMaintenance && event.Result == RuntimeResultFailed {
			failures++
		}
	}
	if failures != 2 {
		t.Fatalf("maintenance failure events = %d, want 2", failures)
	}
}

func correlationDisclosureForTest() correlation.DisclosurePolicy {
	return correlation.DisclosurePolicy{}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestMaintenanceResponseWriterPreserves503AndOptionalBody(t *testing.T) {
	underlying := httptest.NewRecorder()
	writer := &maintenanceResponseWriter{ResponseWriter: underlying}
	if writer.Unwrap() != underlying {
		t.Fatal("Unwrap() did not return the underlying writer")
	}
	writer.WriteHeader(http.StatusTeapot)
	writer.WriteHeader(http.StatusOK)
	if _, err := writer.Write([]byte("body")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if underlying.Code != http.StatusServiceUnavailable || underlying.Body.String() != "body" {
		t.Fatalf("maintenance writer = %d %q", underlying.Code, underlying.Body.String())
	}
	head := &maintenanceResponseWriter{ResponseWriter: httptest.NewRecorder(), head: true}
	if count, err := head.Write([]byte("body")); err != nil || count != 4 {
		t.Fatalf("HEAD Write() = %d, %v", count, err)
	}
	broken := &maintenanceResponseWriter{ResponseWriter: responseWriterFrom(failingWriter{})}
	if _, err := broken.Write([]byte("body")); err == nil {
		t.Fatal("Write() hid the underlying failure")
	}
	emptyRuntime := newMaintenanceRuntime(Maintenance{
		Response: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	}, nil)
	emptyRuntime.state.Store(&MaintenanceState{Enabled: true, Since: time.Now()})
	emptyRecorder := httptest.NewRecorder()
	emptyRuntime.admission(http.NotFoundHandler()).ServeHTTP(
		emptyRecorder,
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	if emptyRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("empty custom handler status = %d", emptyRecorder.Code)
	}
}

func responseWriterFrom(writer io.Writer) http.ResponseWriter {
	return &writerResponse{writer: writer, header: make(http.Header)}
}

type writerResponse struct {
	writer io.Writer
	header http.Header
}

func (writer *writerResponse) Header() http.Header            { return writer.header }
func (*writerResponse) WriteHeader(int)                       {}
func (writer *writerResponse) Write(body []byte) (int, error) { return writer.writer.Write(body) }
