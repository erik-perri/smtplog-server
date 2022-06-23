package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

var listening = false
var smtpConnections []net.Conn

type smtpContextKey string

func ListenForConnections(ctx context.Context) bool {
	listenAddress := fmt.Sprintf("%s:%s", ctx.Value(smtpContextKey("host")), ctx.Value(smtpContextKey("port")))
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

		connectionCtx, _ := context.WithTimeout(ctx, time.Second*20)

		go handleConnection(connection, connectionCtx)
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

func handleConnection(connection net.Conn, ctx context.Context) {
	send220(connection, ctx)

timeout:
	for listening {
		select {
		case <-ctx.Done():
			break timeout
		default:
			log.Printf("Reading %s %s", connection.RemoteAddr(), ctx)
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
	}

	// If we're not listening, the connection would have been closed in StopListening
	if listening {
		removeConnection(connection)
	}
}

func send220(conn net.Conn, ctx context.Context) {
	_, err := conn.Write([]byte(
		fmt.Sprintf(
			"220 %s ESMTP %s\n",
			ctx.Value(smtpContextKey("host")),
			ctx.Value(smtpContextKey("serverName")),
		),
	))

	if err != nil {
		log.Printf("Failed to send 220 to %s", conn.RemoteAddr())
		return
	}
}
