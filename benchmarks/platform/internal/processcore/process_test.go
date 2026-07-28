package processcore_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/processcore"
)

func TestRunPublishesStartupAndStopsBothListeners(t *testing.T) {
	t.Parallel()

	business := listen(t)
	management := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- processcore.Run(ctx, processcore.Config{
			Business:   business,
			Management: management,
			Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write([]byte("business"))
			}),
			ShutdownTimeout: time.Second,
		})
	}()

	assertEventuallyResponse(t, "http://"+management.Addr().String()+"/startupz", "startup")
	assertEventuallyResponse(t, "http://"+business.Addr().String()+"/", "business")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop")
	}
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(
		context.Background(),
		"tcp",
		"127.0.0.1:0",
	)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	return listener
}

func assertEventuallyResponse(t *testing.T, url string, contains string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(url) //nolint:noctx // bounded by the test deadline.
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if readErr == nil && closeErr == nil &&
				response.StatusCode == http.StatusOK &&
				strings.Contains(string(body), contains) {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s did not return %q", url, contains)
}
