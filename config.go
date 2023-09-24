package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
)

type ConfigurationFile struct {
	BannerHost          string `json:"banner_host"`
	BannerName          string `json:"banner_name"`
	CertFile            string `json:"cert_file"`
	ConnectionTimeLimit int    `json:"connection_time_limit"`
	IsTLS               bool   `json:"is_tls"`
	KeyFile             string `json:"key_file"`
	ListenHost          string `json:"listen_host"`
	ListenPort          int    `json:"listen_port"`
	LogConnection       string `json:"log_connection"`
	ReadTimeout         int    `json:"read_timeout"`
}

type Configuration struct {
	BannerHost          string
	BannerName          string
	ConnectionTimeLimit int
	IsTLS               bool
	ListenHost          string
	ListenPort          int
	LogConnection       string
	ReadTimeout         int
	TLSConfig           *tls.Config
}

func LoadConfiguration(file string) (config *Configuration, err error) {
	handle, _ := os.Open(file)

	defer func(handle *os.File) {
		_ = handle.Close()
	}(handle)

	decoder := json.NewDecoder(handle)
	configuration := ConfigurationFile{}

	err = decoder.Decode(&configuration)
	if err != nil {
		return nil, err
	}

	tlsConfig, err := loadTLSConfig(configuration.CertFile, configuration.KeyFile)
	if err != nil {
		return nil, err
	}

	return &Configuration{
		BannerHost:          configuration.BannerHost,
		BannerName:          configuration.BannerName,
		ConnectionTimeLimit: configuration.ConnectionTimeLimit,
		IsTLS:               configuration.IsTLS,
		ListenHost:          configuration.ListenHost,
		ListenPort:          configuration.ListenPort,
		LogConnection:       configuration.LogConnection,
		ReadTimeout:         configuration.ReadTimeout,
		TLSConfig:           tlsConfig,
	}, nil
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
