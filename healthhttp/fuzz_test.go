package healthhttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/service/healthhttp"
)

func FuzzHealthPayload(fuzz *testing.F) {
	fuzz.Add("database", uint8(0), int64(0), 0, 0, false)
	fuzz.Add("", uint8(255), int64(-1), -1, -1, true)
	fuzz.Add("queue", uint8(1), int64(time.Second), 1, 1, true)

	fuzz.Fuzz(func(
		t *testing.T,
		name string,
		mode uint8,
		timeout int64,
		maxConcurrency int,
		maxChecks int,
		details bool,
	) {
		probes, err := healthhttp.New(healthhttp.Config{
			Mode:           healthhttp.Mode(mode),
			CheckTimeout:   time.Duration(timeout),
			MaxConcurrency: maxConcurrency,
			MaxChecks:      maxChecks,
			Details:        details,
			Checks: []healthhttp.Check{{
				Name: name,
				Run: func(context.Context) error {
					if len(name)%2 == 0 {
						return nil
					}

					return errors.New("not ready")
				},
			}},
		})
		if err != nil {
			return
		}
		recorder := httptest.NewRecorder()
		probes.Readiness().ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, "/", nil),
		)
		var response healthhttp.Response
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("response JSON error = %v", err)
		}
		if response.Probe != "readiness" {
			t.Fatalf("probe = %q", response.Probe)
		}
	})
}
