package main

import (
	"encoding/json"
	"os"
)

type Configuration struct {
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

func LoadConfiguration(file string) (config *Configuration, err error) {
	handle, _ := os.Open(file)

	defer func(handle *os.File) {
		_ = handle.Close()
	}(handle)

	decoder := json.NewDecoder(handle)
	configuration := Configuration{}

	err = decoder.Decode(&configuration)
	if err != nil {
		return nil, err
	}

	return &configuration, nil
}
