package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"
)

type ConnectionContext struct {
	cancel     context.CancelFunc
	connection net.Conn
	context    context.Context
}

func (n *ConnectionContext) Send220() {
	_, err := n.connection.Write([]byte(
		fmt.Sprintf(
			"220 %s ESMTP %s\n",
			n.context.Value(smtpContextKey("host")),
			n.context.Value(smtpContextKey("serverName")),
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
			err := n.connection.SetReadDeadline(time.Now().Add(time.Second * 5))
			if err != nil {
				log.Printf("Failed to set read deadline, %s", err)
				break
			}

			netData, err := bufio.NewReader(n.connection).ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break done
				}
				log.Printf(
					"Failed to read from from %s, %s",
					n.connection.RemoteAddr(),
					err,
				)
				break
			}

			netData = strings.Trim(netData, "\r\n")
			if len(netData) == 0 {
				break done
			}

			log.Printf("Read %s (%d)", netData, len(netData))
		}
	}
}
