package serverhttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestRunAggregatesForcedCloseFailure(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	server, err := New(
		listener,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(requestEntered)
			<-releaseRequest
		}),
		WithShutdownTimeout(time.Nanosecond),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	closeFailure := errors.New("forced close failed")
	server.close = func() error {
		_ = server.httpServer.Close()

		return closeFailure
	}

	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- server.Run(ctx) }()
	requestResult := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String())
		if requestErr == nil {
			requestErr = response.Body.Close()
		}
		requestResult <- requestErr
	}()
	<-requestEntered
	cancel()
	err = <-runResult
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, closeFailure) {
		t.Fatalf("Run() error = %v, want deadline and close failure", err)
	}
	close(releaseRequest)
	<-requestResult
}
