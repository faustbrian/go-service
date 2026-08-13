// Package referenceplatform provides a non-production service used to verify
// Golib process behavior inside constrained Linux containers.
package referenceplatform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"time"

	"github.com/faustbrian/golib/pkg/service"
)

var (
	// ErrInvalidConfig identifies an incomplete platform reference boundary.
	ErrInvalidConfig = errors.New("reference platform config is invalid")
	// ErrDependencyUnavailable identifies an unhealthy TLS dependency.
	ErrDependencyUnavailable = errors.New("reference platform dependency is unavailable")
)

// Config defines explicit listeners or addresses and the TLS dependency used
// by readiness and runtime verification.
type Config struct {
	// BusinessAddress is used when BusinessListener is not supplied.
	BusinessAddress string
	// ManagementAddress is used when ManagementListener is not supplied.
	ManagementAddress string
	// BusinessListener accepts application traffic when supplied.
	BusinessListener net.Listener
	// ManagementListener accepts health and telemetry traffic when supplied.
	ManagementListener net.Listener
	// DependencyURL is the TLS endpoint exercised by readiness verification.
	DependencyURL string
	// Client performs the dependency readiness request and remains caller-owned.
	Client *http.Client
}

// RuntimeReport exposes only non-sensitive process facts required by the
// disposable platform harness.
type RuntimeReport struct {
	// GOOS identifies the runtime operating system.
	GOOS string `json:"goos"`
	// GOARCH identifies the runtime architecture.
	GOARCH string `json:"goarch"`
	// EffectiveUserID is the process effective user identifier.
	EffectiveUserID int `json:"effective_user_id"`
	// EffectiveGroupID is the process effective group identifier.
	EffectiveGroupID int `json:"effective_group_id"`
	// TemporaryStorage reports whether the runtime storage is disposable.
	TemporaryStorage bool `json:"temporary_storage"`
}

// ResourceReport exposes bounded process measurements used by the disposable
// load harness. OpenFileDescriptors is -1 when the runtime does not expose a
// Linux-compatible process descriptor directory.
type ResourceReport struct {
	// HeapAllocBytes is the currently allocated heap size.
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
	// HeapSysBytes is the heap memory obtained from the operating system.
	HeapSysBytes uint64 `json:"heap_sys_bytes"`
	// Goroutines is the current goroutine count.
	Goroutines int `json:"goroutines"`
	// OpenFileDescriptors is the current descriptor count or -1 when unavailable.
	OpenFileDescriptors int `json:"open_file_descriptors"`
}

// New constructs the platform reference exclusively through public service
// APIs. The supplied client and listeners remain caller-owned.
func New(config Config) (service.Definition, error) {
	if err := validate(config); err != nil {
		return service.Definition{}, err
	}
	handler := newHandler(config)
	readiness := func(ctx context.Context) error {
		return checkDependency(ctx, config)
	}
	return service.Definition{
		Identity: service.Identity{Name: "reference-platform", Version: "1.0.0", Environment: "assurance"},
		Management: service.Management{
			Address: config.ManagementAddress, Listener: config.ManagementListener,
		},
		Commands: service.Commands{Serve: service.CommandFor(service.CommandSpec[struct{}]{
			Name: "serve", Summary: "run the platform reference service", Kind: service.CommandKindLongRunning,
			Load: func(context.Context, service.Invocation) (struct{}, error) { return struct{}{}, nil },
			Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
				return service.Plan{
					HTTP: &service.HTTP{
						Address: config.BusinessAddress, Listener: config.BusinessListener, Handler: handler,
					},
					Readiness: []service.ReadinessCheck{{Name: "tls-dependency", Run: readiness}},
				}, nil
			},
		})},
	}, nil
}

func validate(config Config) error {
	if config.Client == nil || (config.BusinessAddress == "" && config.BusinessListener == nil) ||
		(config.ManagementAddress == "" && config.ManagementListener == nil) {
		return ErrInvalidConfig
	}
	endpoint, err := url.Parse(config.DependencyURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
		return ErrInvalidConfig
	}
	return nil
}

func newHandler(config Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(writer, "reference-platform\n")
	})
	mux.HandleFunc("GET /dependencyz", func(writer http.ResponseWriter, request *http.Request) {
		if err := checkDependency(request.Context(), config); err != nil {
			http.Error(writer, "dependency unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /runtimez", func(writer http.ResponseWriter, _ *http.Request) {
		report := RuntimeReport{
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
			EffectiveUserID: os.Geteuid(), EffectiveGroupID: os.Getegid(),
			TemporaryStorage: verifyTemporaryStorage(),
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(report)
	})
	mux.HandleFunc("GET /resourcesz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(currentResourceReport())
	})
	return mux
}

func currentResourceReport() ResourceReport {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	descriptors := -1
	if entries, err := os.ReadDir("/proc/self/fd"); err == nil {
		descriptors = len(entries)
	}
	return ResourceReport{
		HeapAllocBytes: memory.HeapAlloc, HeapSysBytes: memory.HeapSys,
		Goroutines: runtime.NumGoroutine(), OpenFileDescriptors: descriptors,
	}
}

func checkDependency(ctx context.Context, config Config) error {
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, config.DependencyURL, nil)
	if err != nil {
		return ErrDependencyUnavailable
	}
	response, err := config.Client.Do(request)
	if err != nil {
		return ErrDependencyUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrDependencyUnavailable
	}
	return nil
}

func verifyTemporaryStorage() bool {
	file, err := os.CreateTemp("", "reference-platform-*")
	if err != nil {
		return false
	}
	name := file.Name()
	if _, err = file.WriteString("ephemeral"); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return false
	}
	closed := file.Close() == nil
	removed := os.Remove(name) == nil
	return closed && removed
}
