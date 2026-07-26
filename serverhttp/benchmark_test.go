package serverhttp_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/faustbrian/golib/pkg/service/serverhttp"
)

func BenchmarkRequestMiddleware(benchmark *testing.B) {
	handler, request := benchmarkMiddleware(benchmark)

	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for range benchmark.N {
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
}

func TestRequestMiddlewareAllocationBudget(t *testing.T) {
	handler, request := benchmarkMiddleware(t)
	allocations := testing.AllocsPerRun(100, func() {
		handler.ServeHTTP(httptest.NewRecorder(), request)
	})
	if allocations > 14 {
		t.Fatalf("allocations = %.1f, budget = 14", allocations)
	}
}

type benchmarkTesting interface {
	Helper()
	Fatal(...any)
}

func benchmarkMiddleware(testingContext benchmarkTesting) (http.Handler, *http.Request) {
	testingContext.Helper()

	requestIDs, err := serverhttp.RequestIDs(serverhttp.RequestIDConfig{
		Generator: func() (string, error) { return "benchmark-id", nil },
	})
	if err != nil {
		testingContext.Fatal(err)
	}
	bodyLimit, err := serverhttp.LimitBody(1024)
	if err != nil {
		testingContext.Fatal(err)
	}
	handler, err := serverhttp.Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		serverhttp.Recover(),
		requestIDs,
		bodyLimit,
	)
	if err != nil {
		testingContext.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	return handler, request
}
