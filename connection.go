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
	data string
	from string
	to   []string
}

type Response struct {
	code     int
	partial  bool
	response string
}

type ConnectionContext struct {
	cancelTimeout     context.CancelFunc
	conn              net.Conn
	context           context.Context
	currentMessage    MailMessage
	disconnectWaiting bool
	isReadingData     bool
	text              *textproto.Conn
}

func CommandNotRecognizedResponse() Response {
	return Response{
		code:     500,
		response: "Command not recognized",
	}
}

func OKResponse() Response {
	return Response{
		code:     250,
		response: "OK",
	}
}

func (n *ConnectionContext) SendResponse(response Response) {
	separator := " "
	if response.partial {
		separator = "-"
	}

	err := n.conn.SetWriteDeadline(time.Now().Add(time.Second * 1))
	if err != nil {
		log.Printf("Failed to set write deadline on %s, %s", n.conn.RemoteAddr(), err)
	}

	log.Printf("Sending %d%s%s", response.code, separator, response.response)
	err = n.text.PrintfLine("%d%s%s", response.code, separator, response.response)
	if err != nil {
		log.Printf("Failed to send %d to %s", response.code, n.conn.RemoteAddr())
	}
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
		n.SendResponse(OKResponse())
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
			n.SendResponse(CommandNotRecognizedResponse())
			break
		}

		n.isReadingData = true
		n.SendResponse(Response{
			code:     354,
			response: "End data with <CR><LF>.<CR><LF>",
		})
	case "EHLO":
		n.SendResponse(Response{
			code:    250,
			partial: true,
			response: fmt.Sprintf(
				"Hello %s, I am %s",
				arguments,
				n.context.Value(smtpContextKey("bannerName")),
			),
		})

		n.SendResponse(Response{
			code:     250,
			response: "HELP",
		})
	case "HELO":
		n.SendResponse(Response{
			code: 250,
			response: fmt.Sprintf(
				"Hello %s, I am %s",
				arguments,
				n.context.Value(smtpContextKey("bannerName")),
			),
		})
	case "HELP":
		n.SendResponse(Response{
			code:     214,
			response: "I'm sorry Dave, I'm afraid I can't do that",
		})
	case "MAIL":
		if !strings.HasPrefix(arguments, "FROM:") {
			n.SendResponse(CommandNotRecognizedResponse())
		} else {
			n.currentMessage.from = strings.TrimPrefix(arguments, "FROM:")
			n.SendResponse(OKResponse())
		}
	case "NOOP":
		n.SendResponse(OKResponse())
	case "RCPT":
		if !strings.HasPrefix(arguments, "TO:") {
			n.SendResponse(CommandNotRecognizedResponse())
		} else {
			n.currentMessage.to = append(n.currentMessage.to, strings.TrimPrefix(arguments, "TO:"))
			n.SendResponse(OKResponse())
		}
	case "RSET":
		n.currentMessage = MailMessage{}
		n.SendResponse(OKResponse())
	case "QUIT":
		n.SendResponse(Response{
			code:     221,
			response: "Service closing transmission channel",
		})
		return false
	default:
		n.SendResponse(CommandNotRecognizedResponse())
	}

	return true
}

func (n *ConnectionContext) SendBanner() {
	n.SendResponse(Response{
		code: 220,
		response: fmt.Sprintf(
			"%s ESMTP %s",
			n.context.Value(smtpContextKey("bannerHost")).(string),
			n.context.Value(smtpContextKey("bannerName")).(string),
		),
	})
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

						if n.disconnectWaiting {
							break read
						}

						continue read
					}

					break read
				}
			}

			if n.disconnectWaiting {
				n.SendResponse(Response{
					code:     421,
					response: "Service not available, closing transmission channel",
				})
				break read
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
