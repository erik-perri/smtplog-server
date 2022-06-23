package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx := context.Background()
	ctx = context.WithValue(ctx, smtpContextKey("host"), "localhost")
	ctx = context.WithValue(ctx, smtpContextKey("port"), "2525")
	ctx = context.WithValue(ctx, smtpContextKey("serverName"), "smtp-log")

	setupSignalHandler()
	ListenForConnections(ctx)
}

func setupSignalHandler() {
	signals := make(chan os.Signal)

	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	go func() {
		receivedSignal := <-signals
		log.Printf("Signal received %s", receivedSignal)

		StopListening()

		os.Exit(0)
	}()
}
