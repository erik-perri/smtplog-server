package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/textproto"
	"sync"
	"time"
)

type smtpContextKey string

type SmtpServerContext struct {
	connections []ConnectionContext
	context     context.Context
	listener    net.Listener
	quitChannel chan interface{}
	tlsConfig   *tls.Config
	waitGroup   sync.WaitGroup
}

func CreateListener(
	listenHost string,
	listenPort int,
	tlsConfig *tls.Config,
) (listener net.Listener, err error) {
	listenAddress := fmt.Sprintf("%s:%d", listenHost, listenPort)

	if tlsConfig == nil {
		return net.Listen("tcp", listenAddress)
	}

	return tls.Listen("tcp", listenAddress, tlsConfig)
}

func StartSmtpServer(
	listenHost string,
	listenPort int,
	tlsConfig *tls.Config,
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

	listener, err := CreateListener(listenHost, listenPort, tlsConfig)
	if err != nil {
		return nil, err
	}

	log.Printf("Started listening on %s", listenAddress)
	server = &SmtpServerContext{
		context:     ctx,
		listener:    listener,
		tlsConfig:   tlsConfig,
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

listen:
	for {
		select {
		case <-n.quitChannel:
			break listen
		case <-n.context.Done():
			break listen
		default:
			conn, err := n.listener.Accept()
			if err != nil {
				select {
				case <-n.quitChannel:
					break listen
				case <-n.context.Done():
					break listen
				default:
					log.Printf("Failed to accept connection, %s", err)
					continue listen
				}
			}

			log.Printf("Accepted connection from %s", conn.RemoteAddr())

			ctx, cancel := context.WithTimeout(
				n.context,
				n.context.Value(smtpContextKey("connectionTimeLimit")).(time.Duration),
			)

			textConn := textproto.NewConn(conn)

			connection := ConnectionContext{
				cancelTimeout: cancel,
				conn:          conn,
				text:          textConn,
				context:       ctx,
			}

			n.connections = append(n.connections, connection)
			n.waitGroup.Add(1)

			go func() {
				connection.SendBanner()
				connection.WaitForCommands()

				n.Close(connection)
			}()
			break
		}
	}
}

func (n *SmtpServerContext) Close(connection ConnectionContext) {
	connection.cancelTimeout()

	err := connection.text.Close()
	if err != nil {
		log.Printf(
			"Failed to close connection %s, %s",
			connection.conn.RemoteAddr(),
			err,
		)
	}

	n.connections = removeConnectionFromContextArray(n.connections, connection)
	n.waitGroup.Done()
}

func (n *SmtpServerContext) CloseConnections() {
	for _, connection := range n.connections {
		// If we're a TLS server we can't send a 421 response or we risk hanging on the send if the TLS connection is
		// not yet established.
		// TODO Can we detect if the TLS connection is established?
		if n.tlsConfig == nil {
			// TODO This needs to queue up 421 as an immediate response to any incoming command, then wait rather than
			//      sending immediately
			connection.SendResponse(Response{
				code:     421,
				response: "Service not available, closing transmission channel",
			})
		}
		n.Close(connection)
	}
}

func removeConnectionFromContextArray(connections []ConnectionContext, remove ConnectionContext) []ConnectionContext {
	var filtered []ConnectionContext
	for _, connection := range connections {
		if connection.conn != remove.conn {
			filtered = append(filtered, connection)
		}
	}
	return filtered
}
