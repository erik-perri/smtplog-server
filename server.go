package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"
)

type smtpContextKey string

type SmtpServerContext struct {
	connections []ConnectionContext
	context     context.Context
	cancel      context.CancelFunc
	listener    net.Listener
}

func StartServer(
	listenHost string,
	listenPort int,
) (server *SmtpServerContext, err error) {
	listenAddress := fmt.Sprintf("%s:%d", listenHost, listenPort)

	ctx := context.Background()
	ctx = context.WithValue(ctx, smtpContextKey("address"), listenAddress)
	ctx, cancel := context.WithCancel(ctx)

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		log.Printf("Failed to listen on %s, %s", listenAddress, err)
		cancel()
		return nil, err
	}

	log.Printf("Started listening on %s", listenAddress)
	server = &SmtpServerContext{
		context:  ctx,
		cancel:   cancel,
		listener: listener,
	}

	return server, nil
}

func (n *SmtpServerContext) Stop() {
	n.cancel()

	for _, connection := range n.connections {
		n.Close(connection)
	}

	err := n.listener.Close()
	if err != nil {
		log.Printf("Failed to close listener, %s", err)
	}
}

func (n *SmtpServerContext) WaitForConnections() {
done:
	for {
		select {
		case <-n.context.Done():
			break done
		default:
			conn, err := n.listener.Accept()
			if err != nil {
				log.Printf("Failed to accept connection, %s", err)
				break
			}

			log.Printf("Accepted connection from %s", conn.RemoteAddr())

			ctx, cancel := context.WithTimeout(n.context, time.Second*10)

			connection := ConnectionContext{
				cancel:     cancel,
				connection: conn,
				context:    ctx,
			}

			n.connections = append(n.connections, connection)

			go func() {
				connection.Send220()
				connection.WaitForCommands()

				n.Close(connection)
			}()
			break
		}
	}
}

func (n *SmtpServerContext) Close(connection ConnectionContext) {
	n.connections = removeConnectionFromContextArray(n.connections, connection)

	connection.cancel()

	err := connection.connection.Close()
	if err != nil {
		log.Printf(
			"Failed to close connection %s, %s",
			connection.connection.RemoteAddr(),
			err,
		)
	}
}

func removeConnectionFromContextArray(connections []ConnectionContext, remove ConnectionContext) []ConnectionContext {
	var filtered []ConnectionContext
	for _, connection := range connections {
		if connection.connection != remove.connection {
			filtered = append(filtered, connection)
		}
	}
	return filtered
}
