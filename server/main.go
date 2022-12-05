package main

import (
	"context"
	"net"
	"os/signal"
	"sync"
	"syscall"

	"google.golang.org/grpc"

	"github.com/fardream/jobsched"
)

func main() {
	lis, err := net.Listen("tcp", ":5050")
	if err != nil {
		panic(err)
	}

	s := grpc.NewServer()

	server := jobsched.NewServer()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT)
	defer cancel()

	jobsched.RegisterJobSchedServer(s, server)

	var wg sync.WaitGroup
	defer wg.Wait()

	wg.Add(1)

	go func() {
		defer wg.Done()
		server.MainLoop(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.Serve(lis); err != nil {
			panic(err)
		}
	}()

	<-ctx.Done()

	s.Stop()
}
