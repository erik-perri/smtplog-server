package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	configFile := flag.String("config", "", "Configuration file")
	flag.Parse()

	if *configFile == "" {
		log.Fatalf("No configuration file specified")
	}

	config, err := LoadConfiguration(*configFile)
	if err != nil {
		log.Fatalf("Failed to load configuration %s", err)
	}

	ctx, stop := context.WithCancel(context.Background())

	logger, err := CreateDatabaseLogger(ctx, config.LogConnection)
	if err != nil {
		log.Fatalf("Failed to initialize database connection %s", err)
	}

	server, err := CreateSMTPServer(ctx, config, logger)
	if err != nil {
		log.Fatalf("Failed to start server %s", err)
	}

	defer stop()

	defer func(logger *DatabaseLogger) {
		err := logger.Close()
		if err != nil {
			log.Printf("Failed to close database connection %s", err)
		}
	}(logger)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-signals

		server.Stop()

		shutdownTimeout := config.ConnectionTimeLimit + 1
		log.Printf("Waiting %d seconds for graceful shutdown", shutdownTimeout)
		time.Sleep(time.Duration(shutdownTimeout) * time.Second)

		log.Printf("Forcing connections closed")
		server.CloseConnections()

		log.Fatalf("Failed to cleanup in time")
	}()

	server.WaitForConnections()
	server.WaitForCleanup()
}
