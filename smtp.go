package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
)

var listening = false
var smtpConnections []net.Conn

func ListenForConnections(host string, port string) bool {
	listenAddress := fmt.Sprintf("%s:%s", host, port)
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		log.Printf("Failed to listen on %s, %s", listenAddress, err)
		os.Exit(1)
		return false
	}

	log.Printf("Waiting for connections on %s", listenAddress)

	defer func(listener net.Listener) {
		err := listener.Close()
		if err != nil {
			log.Printf("Failed to close listener, %s", err)
		}
	}(listener)

	listening = true

	for listening {
		connection, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection, %s", err)
			continue
		}

		log.Printf("Accepted connection from %s", connection.RemoteAddr())
		smtpConnections = append(smtpConnections, connection)
		go handleConnection(connection)
	}

	return true
}

func StopListening() {
	listening = false

	for _, connection := range smtpConnections {
		removeConnection(connection)
	}
}

func removeConnection(connection net.Conn) {
	err := connection.Close()
	if err != nil {
		log.Printf("Failed to close connection %s, %s", connection.RemoteAddr(), err)
	} else {
		log.Printf("Closed connection %s", connection.RemoteAddr())
	}

	var filteredConnections []net.Conn
	for _, storedConnection := range smtpConnections {
		if storedConnection != connection {
			filteredConnections = append(filteredConnections, storedConnection)
		}
	}
	smtpConnections = filteredConnections
}

func handleConnection(connection net.Conn) {
	for listening {
		log.Printf("Reading %s", connection.RemoteAddr())
		netData, err := bufio.NewReader(connection).ReadString('\n')
		if err != nil {
			if listening {
				log.Printf("Failed to read from from %s, %s", connection.RemoteAddr(), err)
			}
			break
		}

		netData = strings.Trim(netData, "\r\n")
		if len(netData) == 0 {
			break
		}

		log.Printf("Read %s (%d)", netData, len(netData))
	}

	// If we're not listening, the connection would have been closed in StopListening
	if listening {
		removeConnection(connection)
	}
}
