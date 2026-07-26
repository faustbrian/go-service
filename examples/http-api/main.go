package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/faustbrian/golib/pkg/service/healthhttp"
	"github.com/faustbrian/golib/pkg/service/serverhttp"
	"github.com/faustbrian/golib/pkg/service/service"
)

func main() {
	runtime, err := service.New(service.Config{})
	if err != nil {
		log.Fatal(err)
	}
	probes, err := healthhttp.New(healthhttp.Config{Lifecycle: runtime})
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /live", probes.Liveness())
	mux.Handle("GET /startup", probes.Startup())
	mux.Handle("GET /ready", probes.Readiness())
	mux.HandleFunc("GET /", func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("go-service\n"))
	})
	address := os.Getenv("LISTEN_ADDRESS")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	listener, err := (&net.ListenConfig{}).Listen(
		context.Background(),
		"tcp",
		address,
	)
	if err != nil {
		log.Fatal(err)
	}
	server, err := serverhttp.New(
		listener,
		mux,
		serverhttp.WithShutdownTimeout(20*time.Second),
	)
	if err != nil {
		_ = listener.Close()
		log.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	if err := runtime.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
	if err := runtime.Go("http", server.Run); err != nil {
		log.Fatal(err)
	}
	if err := service.Wait(context.Background(), runtime, service.RunConfig{}); err != nil {
		log.Fatal(err)
	}
}
