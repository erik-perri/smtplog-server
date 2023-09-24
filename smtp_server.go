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

type SMTPServer struct {
	connections []*SMTPConnection
	context     context.Context
	listener    net.Listener
	quitChannel chan interface{}
	waitGroup   sync.WaitGroup
}

func CreateSMTPServer(config *Configuration, tlsConfig *tls.Config) (server *SMTPServer, err error) {
	listenAddress := fmt.Sprintf("%s:%d", config.ListenHost, config.ListenPort)

	ctx := context.Background()
	ctx = context.WithValue(ctx, smtpContextKey("address"), listenAddress)
	ctx = context.WithValue(ctx, smtpContextKey("bannerHost"), config.BannerHost)
	ctx = context.WithValue(ctx, smtpContextKey("bannerName"), config.BannerName)
	ctx = context.WithValue(ctx, smtpContextKey("connectionTimeLimit"), config.ConnectionTimeLimit)
	ctx = context.WithValue(ctx, smtpContextKey("readTimeout"), config.ReadTimeout)
	ctx = context.WithValue(ctx, smtpContextKey("tlsConfig"), tlsConfig)

	listener, err := createListener(config.ListenHost, config.ListenPort, config.IsTLS, tlsConfig)
	if err != nil {
		return nil, err
	}

	log.Printf("Started listening on %s", listenAddress)
	server = &SMTPServer{
		context:     ctx,
		listener:    listener,
		quitChannel: make(chan interface{}),
	}

	return server, nil
}

func createListener(
	listenHost string,
	listenPort int,
	isTLS bool,
	tlsConfig *tls.Config,
) (listener net.Listener, err error) {
	listenAddress := fmt.Sprintf("%s:%d", listenHost, listenPort)

	if !isTLS {
		return net.Listen("tcp", listenAddress)
	}

	return tls.Listen("tcp", listenAddress, tlsConfig)
}

func (n *SMTPServer) Stop() {
	close(n.quitChannel)

	for _, connection := range n.connections {
		connection.isDisconnecting = true
	}

	err := n.listener.Close()
	if err != nil {
		log.Printf("Failed to close listener, %s", err)
	}
}

func (n *SMTPServer) WaitForCleanup() {
	n.waitGroup.Wait()
}

func (n *SMTPServer) WaitForConnections() {
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
				time.Duration(n.context.Value(smtpContextKey("connectionTimeLimit")).(int))*time.Second,
			)

			textConn := textproto.NewConn(conn)

			connection := SMTPConnection{
				context:        ctx,
				cancel:         cancel,
				netConnection:  conn,
				textConnection: textConn,
			}

			n.connections = append(n.connections, &connection)

			n.waitGroup.Add(1)

			go func() {
				connection.SendBanner()
				connection.WaitForCommands()

				n.Close(&connection)
			}()
			break
		}
	}
}

func (n *SMTPServer) Close(connection *SMTPConnection) {
	connection.cancel()

	err := connection.textConnection.Close()
	if err != nil {
		log.Printf(
			"Failed to close connection %s, %s",
			connection.netConnection.RemoteAddr(),
			err,
		)
	}

	n.connections = removeConnection(n.connections, connection)
	n.waitGroup.Done()
}

func (n *SMTPServer) CloseConnections() {
	for _, connection := range n.connections {
		n.Close(connection)
	}
}

func removeConnection(connections []*SMTPConnection, remove *SMTPConnection) []*SMTPConnection {
	var filtered []*SMTPConnection
	for _, connection := range connections {
		if connection.netConnection != remove.netConnection {
			filtered = append(filtered, connection)
		}
	}
	return filtered
}
