package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	server, err := StartSmtpServer(
		"localhost",
		2525,
		time.Second*10,
		time.Second*5,
		"localhost",
		"smtp-log",
	)
	if err != nil {
		log.Fatalf("Failed to start server %s", err)
	}

	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-signals

		server.Stop()

		gracefulShutdownTime := 5 * time.Second
		log.Printf("Waiting %s seconds for graceful shutdown", gracefulShutdownTime)
		time.Sleep(gracefulShutdownTime)

		server.CloseConnections()
		log.Fatalf("Failed to cleanup in time")
	}()

	server.WaitForConnections()
	server.WaitForCleanup()
}
