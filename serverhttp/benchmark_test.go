package serverhttp_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/faustbrian/golib/pkg/correlation"
	httpcorrelation "github.com/faustbrian/golib/pkg/correlation/http"
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
	if testing.CoverMode() != "" {
		t.Skip("coverage instrumentation changes allocation counts")
	}

	handler, request := benchmarkMiddleware(t)
	allocations := testing.AllocsPerRun(100, func() {
		handler.ServeHTTP(httptest.NewRecorder(), request)
	})
	if allocations > 20 {
		t.Fatalf("allocations = %.1f, budget = 20", allocations)
	}
}

type benchmarkTesting interface {
	Helper()
	Fatal(...any)
}

type benchmarkGenerator struct{}

func (benchmarkGenerator) New() (string, error) { return "benchmark-id", nil }

func benchmarkMiddleware(testingContext benchmarkTesting) (http.Handler, *http.Request) {
	testingContext.Helper()

	factory, err := correlation.NewFactory(correlation.FactoryOptions{
		Generator: benchmarkGenerator{},
	})
	if err != nil {
		testingContext.Fatal(err)
	}
	identity, err := httpcorrelation.New(factory, httpcorrelation.Options{})
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
		identity.Wrap,
		bodyLimit,
	)
	if err != nil {
		testingContext.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	return handler, request
}
