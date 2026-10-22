package healthhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/faustbrian/go-service/v3/healthhttp"
)

func TestStandaloneProbeDeadlineScopeAndParentCancellation(t *testing.T) {
	for _, test := range []struct {
		name         string
		mode         healthhttp.Mode
		parentBudget time.Duration
		wantElapsed  time.Duration
	}{
		{"concurrent shared probe budget", healthhttp.ModeConcurrent, 0, time.Second},
		{"sequential original per-check budget", healthhttp.ModeSequential, 0, 2 * time.Second},
		{"concurrent shorter request budget", healthhttp.ModeConcurrent, 500 * time.Millisecond, 500 * time.Millisecond},
		{"sequential shorter request budget", healthhttp.ModeSequential, 500 * time.Millisecond, 500 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				check := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
				probes, err := healthhttp.New(healthhttp.Config{
					Mode: test.mode, CheckTimeout: time.Second, MaxConcurrency: 2, Details: true,
					Checks: []healthhttp.Check{{Name: "first", Run: check}, {Name: "second", Run: check}},
				})
				if err != nil {
					t.Fatal(err)
				}
				ctx := t.Context()
				if test.parentBudget != 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, test.parentBudget)
					defer cancel()
				}
				request := httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx)
				writer := httptest.NewRecorder()
				started := time.Now()
				probes.Readiness().ServeHTTP(writer, request)
				if elapsed := time.Since(started); elapsed != test.wantElapsed {
					t.Errorf("probe elapsed = %v, want %v", elapsed, test.wantElapsed)
				}
				var response healthhttp.Response
				if err := json.Unmarshal(writer.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				want := []healthhttp.CheckResult{{Name: "first", Status: "unavailable"}, {Name: "second", Status: "unavailable"}}
				if writer.Code != http.StatusServiceUnavailable || response.Status != "unavailable" || !reflect.DeepEqual(response.Checks, want) {
					t.Errorf("probe lost ordered unavailable results: status=%d response=%+v", writer.Code, response)
				}
				synctest.Wait()
			})
		})
	}
}
