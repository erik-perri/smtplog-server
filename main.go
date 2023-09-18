package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	connectionTimeout := time.Second * 10
	readTimeout := time.Second * 5
	server, err := StartSmtpServer(
		"0.0.0.0",
		2525,
		connectionTimeout,
		readTimeout,
		"localhost",
		"smtp-log",
	)
	if err != nil {
		log.Fatalf("Failed to start server %s", err)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-signals

		server.Stop()

		shutdownTimeout := connectionTimeout + time.Second
		log.Printf("Waiting %s seconds for graceful shutdown", shutdownTimeout)
		time.Sleep(shutdownTimeout)

		server.CloseConnections()
		log.Fatalf("Failed to cleanup in time")
	}()

	server.WaitForConnections()
	server.WaitForCleanup()
}
