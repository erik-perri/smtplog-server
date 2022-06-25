package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

type smtpContextKey string

type SmtpServerContext struct {
	connections []ConnectionContext
	context     context.Context
	listener    net.Listener
	quitChannel chan interface{}
	waitGroup   sync.WaitGroup
}

func StartSmtpServer(
	listenHost string,
	listenPort int,
	connectionTimeLimit time.Duration,
	readDeadline time.Duration,
	bannerHost string,
	bannerName string,
) (server *SmtpServerContext, err error) {
	listenAddress := fmt.Sprintf("%s:%d", listenHost, listenPort)

	ctx := context.Background()
	ctx = context.WithValue(ctx, smtpContextKey("address"), listenAddress)
	ctx = context.WithValue(ctx, smtpContextKey("connectionTimeLimit"), connectionTimeLimit)
	ctx = context.WithValue(ctx, smtpContextKey("readDeadline"), readDeadline)
	ctx = context.WithValue(ctx, smtpContextKey("bannerHost"), bannerHost)
	ctx = context.WithValue(ctx, smtpContextKey("bannerName"), bannerName)

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return nil, err
	}

	log.Printf("Started listening on %s", listenAddress)
	server = &SmtpServerContext{
		context:     ctx,
		listener:    listener,
		quitChannel: make(chan interface{}),
	}

	return server, nil
}

func (n *SmtpServerContext) Stop() {
	close(n.quitChannel)

	err := n.listener.Close()
	if err != nil {
		log.Printf("Failed to close listener, %s", err)
	}
}

func (n *SmtpServerContext) WaitForCleanup() {
	n.waitGroup.Wait()
}

func (n *SmtpServerContext) WaitForConnections() {
	n.waitGroup.Add(1)
	defer func() {
		n.waitGroup.Done()
	}()

done:
	for {
		select {
		case <-n.quitChannel:
			break done
		case <-n.context.Done():
			break done
		default:
			conn, err := n.listener.Accept()
			if err != nil {
				select {
				case <-n.quitChannel:
					break done
				case <-n.context.Done():
					break done
				default:
					log.Printf("Failed to accept connection, %s", err)
					continue done
				}
			}

			log.Printf("Accepted connection from %s", conn.RemoteAddr())

			ctx, cancel := context.WithTimeout(
				n.context,
				n.context.Value(smtpContextKey("connectionTimeLimit")).(time.Duration),
			)

			connection := ConnectionContext{
				cancelTimeout: cancel,
				connection:    conn,
				context:       ctx,
			}

			n.connections = append(n.connections, connection)
			n.waitGroup.Add(1)

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
	connection.cancelTimeout()

	err := connection.connection.Close()
	if err != nil {
		log.Printf(
			"Failed to close connection %s, %s",
			connection.connection.RemoteAddr(),
			err,
		)
	}

	n.connections = removeConnectionFromContextArray(n.connections, connection)
	n.waitGroup.Done()
}

func (n *SmtpServerContext) CloseConnections() {
	for _, connection := range n.connections {
		n.Close(connection)
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
