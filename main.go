package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

const (
	SmtpHost = "localhost"
	SmtpPort = "2525"
)

func main() {
	setupSignalHandler()
	ListenForConnections(SmtpHost, SmtpPort)
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
