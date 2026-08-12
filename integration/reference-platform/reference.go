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
	BusinessAddress    string
	ManagementAddress  string
	BusinessListener   net.Listener
	ManagementListener net.Listener
	DependencyURL      string
	Client             *http.Client
}

// RuntimeReport exposes only non-sensitive process facts required by the
// disposable platform harness.
type RuntimeReport struct {
	GOOS             string `json:"goos"`
	GOARCH           string `json:"goarch"`
	EffectiveUserID  int    `json:"effective_user_id"`
	EffectiveGroupID int    `json:"effective_group_id"`
	TemporaryStorage bool   `json:"temporary_storage"`
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
	return mux
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
