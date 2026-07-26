package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/rpc"

	"github.com/faustbrian/golib/pkg/service/serverhttp"
	"github.com/faustbrian/golib/pkg/service/service"
)

type statusService struct{}

func (statusService) Ping(_ string, reply *string) error {
	*reply = "pong"

	return nil
}

func main() {
	rpcServer := rpc.NewServer()
	if err := rpcServer.RegisterName("Status", statusService{}); err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(rpc.DefaultRPCPath, rpcServer)
	listener, err := (&net.ListenConfig{}).Listen(
		context.Background(),
		"tcp",
		"127.0.0.1:8080",
	)
	if err != nil {
		log.Fatal(err)
	}
	server, err := serverhttp.New(listener, mux)
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
	if err := runtime.Go("rpc", server.Run); err != nil {
		log.Fatal(err)
	}
	if err := service.Wait(context.Background(), runtime, service.RunConfig{}); err != nil {
		log.Fatal(err)
	}
}
