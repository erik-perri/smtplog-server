package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	server, err := StartServer("localhost", 2525)
	if err != nil {
		log.Printf("Failed to start server %s", err)
		os.Exit(1)
		return
	}

	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	go func() {
		receivedSignal := <-signals
		log.Printf("Signal received %s", receivedSignal)

		server.Stop()

		// `Stop` will cause WaitForConnections to close, ending the app. Just in case it doesn't we'll kill
		// ourselves after 3 seconds.
		time.Sleep(3 * time.Second)
		log.Printf("Killing")
		os.Exit(1)
	}()

	server.WaitForConnections()
	log.Printf("Finished")
}
