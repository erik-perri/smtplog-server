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

func main() {
	tlsConfig, err := loadTlsConfig("server.crt", "server.key")
	if err != nil {
		log.Fatalf("Failed to load key pair %s", err)
	}

	connectionTimeLimit := time.Second * 10
	readTimeout := time.Second * 5

	servers, err := createServers(
		tlsConfig,
		connectionTimeLimit,
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

		for _, server := range servers {
			server.Stop()
		}

		shutdownTimeout := connectionTimeLimit + time.Second
		log.Printf("Waiting %s seconds for graceful shutdown", shutdownTimeout)
		time.Sleep(shutdownTimeout)

		for _, server := range servers {
			server.CloseConnections()
		}

		log.Fatalf("Failed to cleanup in time")
	}()

	waitForServerConnections(servers)
	waitForServerCleanup(servers)
}

func loadTlsConfig(certFile string, keyFile string) (*tls.Config, error) {
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

func createServers(
	tlsConfig *tls.Config,
	connectionTimeLimit time.Duration,
	readTimeout time.Duration,
	bannerHost string,
	bannerName string,
) ([]*SmtpServerContext, error) {
	servers := make([]*SmtpServerContext, 0)

	if tlsConfig != nil {
		server, err := StartSmtpServer(
			"0.0.0.0",
			587,
			tlsConfig,
			connectionTimeLimit,
			readTimeout,
			bannerHost,
			bannerName,
		)
		if err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}

	server, err := StartSmtpServer(
		"0.0.0.0",
		25,
		nil,
		connectionTimeLimit,
		readTimeout,
		bannerHost,
		bannerName,
	)
	if err != nil {
		return nil, err
	}
	servers = append(servers, server)

	return servers, nil
}

func waitForServerConnections(servers []*SmtpServerContext) {
	var serverWaitGroup sync.WaitGroup

	for _, server := range servers {
		serverWaitGroup.Add(1)

		go func(s *SmtpServerContext) {
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

func waitForServerCleanup(servers []*SmtpServerContext) {
	var serverWaitGroup sync.WaitGroup

	for _, server := range servers {
		serverWaitGroup.Add(1)

		go func(s *SmtpServerContext) {
			defer serverWaitGroup.Done()
			s.waitGroup.Wait()
		}(server)
	}

	for _, server := range servers {
		go server.WaitForCleanup()
	}

	serverWaitGroup.Wait()
}
