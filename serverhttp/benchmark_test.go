package serverhttp_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/faustbrian/go-service/serverhttp"
)

func BenchmarkRequestMiddleware(benchmark *testing.B) {
	requestIDs, err := serverhttp.RequestIDs(serverhttp.RequestIDConfig{
		Generator: func() (string, error) { return "benchmark-id", nil },
	})
	if err != nil {
		benchmark.Fatal(err)
	}
	bodyLimit, err := serverhttp.LimitBody(1024)
	if err != nil {
		benchmark.Fatal(err)
	}
	handler, err := serverhttp.Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		serverhttp.Recover(),
		requestIDs,
		bodyLimit,
	)
	if err != nil {
		benchmark.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for range benchmark.N {
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
}
