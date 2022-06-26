package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/textproto"
	"time"
)

type ConnectionContext struct {
	cancelTimeout context.CancelFunc
	conn          net.Conn
	text          *textproto.Conn
	context       context.Context
}

func (n *ConnectionContext) SendResponse(code int, response string) {
	err := n.conn.SetWriteDeadline(time.Now().Add(time.Second * 1))
	if err != nil {
		log.Printf("Failed to set write deadline on %s, %s", n.conn.RemoteAddr(), err)
	}

	err = n.text.PrintfLine("%d %s", code, response)
	if err != nil {
		log.Printf("Failed to send %d to %s", code, n.conn.RemoteAddr())
	}
}

func (n *ConnectionContext) Send220() {
	n.SendResponse(
		220,
		fmt.Sprintf(
			"%s Service ready %s",
			n.context.Value(smtpContextKey("bannerHost")),
			n.context.Value(smtpContextKey("bannerName")),
		),
	)
}

func (n *ConnectionContext) WaitForCommands() {
read:
	for {
		select {
		case <-n.context.Done():
			break read
		default:
			err := n.conn.SetReadDeadline(time.Now().Add(
				n.context.Value(smtpContextKey("readDeadline")).(time.Duration),
			))
			if err != nil {
				select {
				case <-n.context.Done():
					break read
				default:
					log.Printf("Failed to set read deadline, %s", err)
					break read
				}
			}

			netData, err := n.text.ReadLine()
			if err != nil {
				select {
				case <-n.context.Done():
					break read
				default:
					if opErr, castSuccess := err.(*net.OpError); castSuccess && opErr.Temporary() {
						if !opErr.Timeout() {
							log.Printf("Failed to read from connection, %s", err)
							time.Sleep(time.Millisecond * 100)
						}
						continue read
					} else {
						break read
					}
				}
			}

			if len(netData) == 0 {
				break read
			}

			log.Printf("Read %s (%d)", netData, len(netData))
		}
	}
}
