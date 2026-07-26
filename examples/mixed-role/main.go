package main

import (
	"context"
	"log"
	"net"
	"net/http"

	"github.com/faustbrian/golib/pkg/service/serverhttp"
	"github.com/faustbrian/golib/pkg/service/service"
)

func main() {
	listener, err := (&net.ListenConfig{}).Listen(
		context.Background(),
		"tcp",
		"127.0.0.1:8080",
	)
	if err != nil {
		log.Fatal(err)
	}
	server, err := serverhttp.New(listener, http.NewServeMux())
	if err != nil {
		_ = listener.Close()
		log.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	runtime, err := service.New(service.Config{})
	if err != nil {
		log.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
	if err := runtime.Go("http", server.Run); err != nil {
		log.Fatal(err)
	}
	for _, task := range []string{"consumer", "processor", "scheduler"} {
		if err := runtime.Go(task, waitForCancellation); err != nil {
			log.Fatal(err)
		}
	}
	if err := service.Wait(context.Background(), runtime, service.RunConfig{}); err != nil {
		log.Fatal(err)
	}
}

func waitForCancellation(ctx context.Context) error {
	<-ctx.Done()

	return nil
}
