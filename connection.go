package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/textproto"
	"strings"
	"time"
)

type MailMessage struct {
	from string
	to   []string
	data string
}

type Response struct {
	code     int
	response string
	partial  bool
}

type ConnectionContext struct {
	cancelTimeout  context.CancelFunc
	conn           net.Conn
	context        context.Context
	currentMessage MailMessage
	isReadingData  bool
	text           *textproto.Conn
}

func (r *Response) Send(connectionContext ConnectionContext) {
	separator := " "
	if r.partial {
		separator = "-"
	}

	err := connectionContext.conn.SetWriteDeadline(time.Now().Add(time.Second * 1))
	if err != nil {
		log.Printf("Failed to set write deadline on %s, %s", connectionContext.conn.RemoteAddr(), err)
	}

	log.Printf("Sending %d%s%s", r.code, separator, r.response)
	err = connectionContext.text.PrintfLine("%d%s%s", r.code, separator, r.response)
	if err != nil {
		log.Printf("Failed to send %d to %s", r.code, connectionContext.conn.RemoteAddr())
	}
}

func (n *ConnectionContext) Send220() {
	(&Response{
		code: 220,
		response: fmt.Sprintf(
			"%s ESMTP %s",
			n.context.Value(smtpContextKey("bannerHost")),
			n.context.Value(smtpContextKey("bannerName")),
		),
	}).Send(*n)
}

func (n *ConnectionContext) Send421() {
	// TODO This needs to queue up 421 as an immediate response to any incoming command rather than sending immediately
	(&Response{
		code:     421,
		response: "Service not available, closing transmission channel",
	}).Send(*n)
}

func (n *ConnectionContext) Send500() {
	(&Response{
		code:     500,
		response: "Command not recognized",
	}).Send(*n)
}

func (n *ConnectionContext) SendOK() {
	(&Response{
		code:     250,
		response: "OK",
	}).Send(*n)
}

func (n *ConnectionContext) HandleData(input string) {
	if input == "." {
		n.isReadingData = false
		log.Printf(
			"Received %d byte message from %s to %s",
			len(n.currentMessage.data),
			n.currentMessage.from,
			n.currentMessage.to,
		)
		n.SendOK()
	} else {
		n.currentMessage.data += strings.TrimPrefix(input, ".") + "\n"
	}
}

func (n *ConnectionContext) HandleCommand(input string) bool {
	parts := strings.SplitN(input, " ", 2)
	if len(parts) == 1 {
		parts = append(parts, "")
	}

	command, arguments := parts[0], parts[1]
	command = strings.ToUpper(command)

	switch command {
	case "DATA":
		if len(n.currentMessage.from) == 0 || len(n.currentMessage.to) == 0 {
			n.Send500()
			break
		}

		n.isReadingData = true
		(&Response{
			code:     354,
			response: "End data with <CR><LF>.<CR><LF>",
		}).Send(*n)
	case "EHLO":
		(&Response{
			code: 250,
			response: fmt.Sprintf(
				"Hello %s, I am %s",
				arguments,
				n.context.Value(smtpContextKey("bannerName")),
			),
			partial: true,
		}).Send(*n)

		(&Response{
			code:     250,
			response: "HELP",
		}).Send(*n)
	case "HELO":
		(&Response{
			code: 250,
			response: fmt.Sprintf(
				"Hello %s, I am %s",
				arguments,
				n.context.Value(smtpContextKey("bannerName")),
			),
		}).Send(*n)
	case "HELP":
		(&Response{
			code:     214,
			response: "I'm sorry Dave, I'm afraid I can't do that",
		}).Send(*n)
	case "MAIL":
		if !strings.HasPrefix(arguments, "FROM:") {
			n.Send500()
		} else {
			n.currentMessage.from = strings.TrimPrefix(arguments, "FROM:")
			n.SendOK()
		}
	case "NOOP":
		n.SendOK()
	case "RCPT":
		if !strings.HasPrefix(arguments, "TO:") {
			n.Send500()
		} else {
			n.currentMessage.to = append(n.currentMessage.to, strings.TrimPrefix(arguments, "TO:"))
			n.SendOK()
		}
	case "RSET":
		n.currentMessage = MailMessage{}
		n.SendOK()
	case "QUIT":
		(&Response{
			code:     221,
			response: "Service closing transmission channel",
		}).Send(*n)
		return false
	default:
		(&Response{
			code:     500,
			response: "Command not recognized",
		}).Send(*n)
	}

	return true
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
					var opErr *net.OpError
					if errors.As(err, &opErr) && opErr.Temporary() {
						if !opErr.Timeout() {
							log.Printf("Failed to read from connection, %s", err)
							time.Sleep(time.Millisecond * 100)
						}

						continue read
					}

					break read
				}
			}

			if !n.isReadingData && len(netData) == 0 {
				break read
			}

			log.Printf("Read \"%s\" (%d)", netData, len(netData))

			if n.isReadingData {
				n.HandleData(netData)
			} else if !n.HandleCommand(netData) {
				break read
			}
		}
	}
}
