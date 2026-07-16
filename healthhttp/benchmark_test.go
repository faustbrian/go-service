package healthhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/faustbrian/go-service/healthhttp"
)

func BenchmarkReadiness(benchmark *testing.B) {
	probes, err := healthhttp.New(healthhttp.Config{
		Checks: []healthhttp.Check{
			{Name: "database", Run: func(context.Context) error { return nil }},
			{Name: "queue", Run: func(context.Context) error { return nil }},
		},
	})
	if err != nil {
		benchmark.Fatal(err)
	}
	handler := probes.Readiness()
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for range benchmark.N {
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
}
