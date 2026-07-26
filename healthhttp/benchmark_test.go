package healthhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/faustbrian/golib/pkg/service/healthhttp"
)

func BenchmarkReadiness(benchmark *testing.B) {
	handler, request := benchmarkReadiness(benchmark)

	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for range benchmark.N {
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
}

func TestReadinessAllocationBudget(t *testing.T) {
	handler, request := benchmarkReadiness(t)
	allocations := testing.AllocsPerRun(100, func() {
		handler.ServeHTTP(httptest.NewRecorder(), request)
	})
	if allocations > 36 {
		t.Fatalf("allocations = %.1f, budget = 36", allocations)
	}
}

type benchmarkTesting interface {
	Helper()
	Fatal(...any)
}

func benchmarkReadiness(testingContext benchmarkTesting) (http.Handler, *http.Request) {
	testingContext.Helper()

	probes, err := healthhttp.New(healthhttp.Config{
		Checks: []healthhttp.Check{
			{Name: "database", Run: func(context.Context) error { return nil }},
			{Name: "queue", Run: func(context.Context) error { return nil }},
		},
	})
	if err != nil {
		testingContext.Fatal(err)
	}
	handler := probes.Readiness()
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	return handler, request
}
