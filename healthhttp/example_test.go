package healthhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/faustbrian/go-service/healthhttp"
)

func ExampleProbes_Readiness() {
	probes, _ := healthhttp.New(healthhttp.Config{
		Checks: []healthhttp.Check{{
			Name: "database",
			Run:  func(context.Context) error { return nil },
		}},
	})
	recorder := httptest.NewRecorder()
	probes.Readiness().ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/ready", nil),
	)
	fmt.Println(recorder.Code)
	fmt.Print(recorder.Body.String())
	// Output:
	// 200
	// {"status":"ok","probe":"readiness"}
}
