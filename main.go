package main

import (
	"crypto/tls"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type Config struct {
	bannerHost          string
	bannerName          string
	certFile            string
	connectionTimeLimit time.Duration
	keyFile             string
	listenHost          string
	listenHostTLS       string
	listenPort          int
	listenPortTLS       int
	readTimeout         time.Duration
}

func main() {
	config := Config{
		bannerHost:          "localhost",
		bannerName:          "smtp-log",
		certFile:            "server.crt",
		connectionTimeLimit: time.Second * 10,
		keyFile:             "server.key",
		listenHost:          "0.0.0.0",
		listenHostTLS:       "0.0.0.0",
		listenPort:          25,
		listenPortTLS:       587,
		readTimeout:         time.Second * 5,
	}

	servers, err := createServers(config)
	if err != nil {
		log.Fatalf("Failed to start server %s", err)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-signals

		for _, server := range servers {
			server.Stop()
		}

		shutdownTimeout := config.connectionTimeLimit + time.Second
		log.Printf("Waiting %s seconds for graceful shutdown", shutdownTimeout)
		time.Sleep(shutdownTimeout)

		log.Printf("Forcing connections closed")
		for _, server := range servers {
			server.CloseConnections()
		}

		log.Fatalf("Failed to cleanup in time")
	}()

	waitForServerConnections(servers)
	waitForServerCleanup(servers)
}

func createServers(config Config) ([]*SMTPServerContext, error) {
	tlsConfig, err := loadTLSConfig(config.certFile, config.keyFile)
	if err != nil {
		log.Fatalf("Failed to load key pair %s", err)
	}

	servers := make([]*SMTPServerContext, 0)

	if tlsConfig != nil {
		server, err := StartSMTPServer(
			config.listenHostTLS,
			config.listenPortTLS,
			tlsConfig,
			config.connectionTimeLimit,
			config.readTimeout,
			config.bannerHost,
			config.bannerName,
		)
		if err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}

	server, err := StartSMTPServer(
		config.listenHost,
		config.listenPort,
		nil,
		config.connectionTimeLimit,
		config.readTimeout,
		config.bannerHost,
		config.bannerName,
	)
	if err != nil {
		return nil, err
	}
	servers = append(servers, server)

	return servers, nil
}

func loadTLSConfig(certFile string, keyFile string) (*tls.Config, error) {
	if certFile == "" || keyFile == "" {
		return nil, nil
	}

	_, certErr := os.Stat(certFile)
	_, keyErr := os.Stat(keyFile)
	if certErr != nil && keyErr != nil {
		if os.IsNotExist(certErr) && os.IsNotExist(keyErr) {
			return nil, nil
		}

		return nil, certErr
	}

	cer, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cer},
	}, nil
}

func waitForServerConnections(servers []*SMTPServerContext) {
	var serverWaitGroup sync.WaitGroup

	for _, server := range servers {
		serverWaitGroup.Add(1)

		go func(s *SMTPServerContext) {
			defer serverWaitGroup.Done()
			select {
			case <-s.quitChannel:
			case <-s.context.Done():
			}
		}(server)
	}

	for _, server := range servers {
		go server.WaitForConnections()
	}

	serverWaitGroup.Wait()
}

func waitForServerCleanup(servers []*SMTPServerContext) {
	var serverWaitGroup sync.WaitGroup

	for _, server := range servers {
		serverWaitGroup.Add(1)

		go func(s *SMTPServerContext) {
			defer serverWaitGroup.Done()
			s.waitGroup.Wait()
		}(server)
	}

	for _, server := range servers {
		go server.WaitForCleanup()
	}

	serverWaitGroup.Wait()
}
