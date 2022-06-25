package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

type ConnectionContext struct {
	cancelTimeout context.CancelFunc
	connection    net.Conn
	context       context.Context
}

func (n *ConnectionContext) Send220() {
	_, err := n.connection.Write([]byte(
		fmt.Sprintf(
			"220 %s ESMTP %s\n",
			n.context.Value(smtpContextKey("bannerHost")),
			n.context.Value(smtpContextKey("bannerName")),
		),
	))

	if err != nil {
		log.Printf("Failed to send 220 to %s", n.connection.RemoteAddr())
	}
}

func (n *ConnectionContext) WaitForCommands() {
done:
	for {
		select {
		case <-n.context.Done():
			break done
		default:
			err := n.connection.SetReadDeadline(time.Now().Add(
				n.context.Value(smtpContextKey("readDeadline")).(time.Duration),
			))
			if err != nil {
				select {
				case <-n.context.Done():
					break done
				default:
					log.Printf("Failed to set read deadline, %s", err)
					break done
				}
			}

			netData, err := bufio.NewReader(n.connection).ReadString('\n')
			if err != nil {
				select {
				case <-n.context.Done():
					break done
				default:
					if opErr, castSuccess := err.(*net.OpError); castSuccess && opErr.Temporary() {
						if !opErr.Timeout() {
							log.Printf("Failed to read from connection, %s", err)
							time.Sleep(time.Millisecond * 100)
						}
						continue done
					} else {
						break done
					}
				}
			}

			netData = strings.Trim(netData, "\r\n")
			if len(netData) == 0 {
				break done
			}

			log.Printf("Read %s (%d)", netData, len(netData))
		}
	}
}
