package main

import (
	"crypto/tls"
	"flag"
	"fmt"
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

	tlsConfig, err := loadTLSConfig(config.CertFile, config.KeyFile)
	if err != nil {
		log.Fatalf("Failed to load key pair %s", err)
	}

	server, err := StartSMTPServer(config, tlsConfig)
	if err != nil {
		log.Fatalf("Failed to start server %s", err)
	}

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

func loadTLSConfig(certFile string, keyFile string) (*tls.Config, error) {
	if certFile == "" || keyFile == "" {
		return nil, nil
	}

	_, certErr := os.Stat(certFile)
	_, keyErr := os.Stat(keyFile)
	if certErr != nil && keyErr != nil {
		if os.IsNotExist(certErr) && os.IsNotExist(keyErr) {
			return nil, fmt.Errorf("certificate and key file not found")
		}
		if os.IsNotExist(certErr) {
			return nil, fmt.Errorf("certificate file not found")
		}
		if os.IsNotExist(keyErr) {
			return nil, fmt.Errorf("key file not found")
		}

		return nil, certErr
	}

	cer, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	return &tls.Config{ //nolint:gosec
		Certificates: []tls.Certificate{cer},
	}, nil
}
