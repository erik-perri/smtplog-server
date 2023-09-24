package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/textproto"
	"strings"
	"time"
)

type CommandResult int

const (
	CommandResultOK CommandResult = iota
	CommandResultError
	CommandResultDisconnect
)

type SMTPMessage struct {
	data string
	from string
	to   []string
}

type SMTPConnection struct {
	cancel          context.CancelFunc
	context         context.Context
	isDisconnecting bool
	isReadingData   bool
	message         SMTPMessage
	netConnection   net.Conn
	textConnection  *textproto.Conn
}

type SMTPResponse struct {
	code    int
	partial bool
	message string
}

type SMTPResponder struct {
	connection *SMTPConnection
	messages   []*SMTPResponse
}

func sendMessages(connection *SMTPConnection, messages []*SMTPResponse) {
	for _, message := range messages {
		separator := " "
		if message.partial {
			separator = "-"
		}

		err := connection.netConnection.SetWriteDeadline(time.Now().Add(time.Second * 1))
		if err != nil {
			log.Printf("Failed to set write deadline on %s, %s", connection.netConnection.RemoteAddr(), err)
		}

		log.Printf("> %d%s%s", message.code, separator, message.message)
		err = connection.textConnection.PrintfLine("%d%s%s", message.code, separator, message.message)

		// Since 221 is the response to a quit command, we don't want to log it as an error
		// in case it was just a client that closed the connection before reading.
		if err != nil && message.code != 221 {
			log.Printf("Failed to send %d to %s", message.code, connection.netConnection.RemoteAddr())
		}
	}
}

func (n *SMTPResponder) Respond(response *SMTPResponse) {
	n.messages = append(n.messages, response)
}

func (n *SMTPResponder) Flush() {
	if n.messages == nil || len(n.messages) == 0 {
		return
	}

	sendMessages(n.connection, n.messages)
	n.messages = make([]*SMTPResponse, 0)
}

func (n *SMTPConnection) SendBanner() {
	sendMessages(n, []*SMTPResponse{
		{
			code: 220,
			message: fmt.Sprintf(
				"%s ESMTP %s",
				n.context.Value(smtpContextKey("bannerHost")).(string),
				n.context.Value(smtpContextKey("bannerName")).(string),
			),
		},
	})
}

func (n *SMTPConnection) readInput() (string, error) {
	err := n.netConnection.SetReadDeadline(time.Now().Add(
		time.Duration(n.context.Value(smtpContextKey("readTimeout")).(int)) * time.Second,
	))
	if err != nil {
		return "", err
	}

	return n.textConnection.ReadLine()
}

func (n *SMTPConnection) WaitForCommands() {
read:
	for {
		select {
		case <-n.context.Done():
			break read
		default:
			netData, err := n.readInput()
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

						if n.isDisconnecting {
							break read
						}

						continue read
					}

					break read
				}
			}

			log.Printf("< %s", netData)

			if n.HandleCommand(netData) == CommandResultDisconnect {
				break read
			}
		}
	}
}

func (n *SMTPConnection) HandleCommand(input string) CommandResult {
	responder := SMTPResponder{
		connection: n,
		messages:   make([]*SMTPResponse, 0),
	}

	defer responder.Flush()

	if n.isDisconnecting {
		responder.Respond(&SMTPResponse{
			code:    421,
			message: "Service not available, closing transmission channel",
		})
		return CommandResultDisconnect
	}

	if !n.isReadingData && len(input) == 0 {
		return CommandResultDisconnect
	}

	parts := strings.SplitN(input, " ", 2)
	if len(parts) == 1 {
		parts = append(parts, "")
	}

	command, arguments := parts[0], parts[1]
	command = strings.ToUpper(command)

	if n.isReadingData {
		return HandlePayload(&responder, n, input)
	}

	smtpCommands := map[string]func(*SMTPResponder, *SMTPConnection, string) CommandResult{
		"DATA":     HandleDATA,
		"EHLO":     HandleEHLO,
		"HELO":     HandleHELO,
		"HELP":     HandleHELP,
		"MAIL":     HandleMAIL,
		"NOOP":     HandleNOOP,
		"QUIT":     HandleQUIT,
		"RCPT":     HandleRCPT,
		"RSET":     HandleRSET,
		"STARTTLS": HandleSTARTTLS,
	}

	if smtpCommands[command] == nil {
		return HandleUnknownCommand(&responder, n, input)
	}

	return smtpCommands[command](&responder, n, arguments)
}

func HandleDATA(responder *SMTPResponder, connection *SMTPConnection, _ string) CommandResult {
	if len(connection.message.from) == 0 || len(connection.message.to) == 0 {
		responder.Respond(&SMTPResponse{
			code:    503,
			message: "Bad sequence of commands",
		})
		return CommandResultError
	}

	responder.Respond(&SMTPResponse{
		code:    354,
		message: "End data with <CRLF>.<CRLF>",
	})

	connection.isReadingData = true
	return CommandResultOK
}

func HandlePayload(responder *SMTPResponder, connection *SMTPConnection, input string) CommandResult {
	if input == "." {
		connection.isReadingData = false
		log.Printf(
			"< %d byte message from %s to %s",
			len(connection.message.data),
			connection.message.from,
			connection.message.to,
		)
		responder.Respond(&SMTPResponse{
			code:    250,
			message: "OK",
		})
	} else {
		connection.message.data += strings.TrimPrefix(input, ".") + "\n"
	}
	return CommandResultOK
}

func HandleEHLO(responder *SMTPResponder, connection *SMTPConnection, _ string) CommandResult {
	lines := []string{
		connection.context.Value(smtpContextKey("bannerHost")).(string),
		"PIPELINING",
	}

	// TODO Add support for other extensions

	if connection.context.Value(smtpContextKey("tlsConfig")).(*tls.Config) != nil {
		lines = append(lines, "STARTTLS")
	}

	for _, line := range lines {
		responder.Respond(&SMTPResponse{
			code:    250,
			partial: true,
			message: line,
		})
	}

	responder.Respond(&SMTPResponse{
		code:    250,
		message: "HELP",
	})

	return CommandResultOK
}

func HandleHELO(responder *SMTPResponder, connection *SMTPConnection, _ string) CommandResult {
	responder.Respond(&SMTPResponse{
		code:    250,
		message: connection.context.Value(smtpContextKey("bannerHost")).(string),
	})
	return CommandResultOK
}

func HandleHELP(responder *SMTPResponder, _ *SMTPConnection, _ string) CommandResult {
	responder.Respond(&SMTPResponse{
		code:    214,
		message: "I'm sorry Dave, I'm afraid I can't do that",
	})
	return CommandResultOK
}

func HandleMAIL(responder *SMTPResponder, connection *SMTPConnection, arguments string) CommandResult {
	if len(arguments) < 1 {
		responder.Respond(&SMTPResponse{
			code:    501,
			message: "Syntax: MAIL FROM:<address>",
		})
		return CommandResultError
	}

	connection.message.from = strings.TrimPrefix(arguments, "FROM:")
	responder.Respond(&SMTPResponse{
		code:    250,
		message: "OK",
	})
	return CommandResultOK
}

func HandleNOOP(responder *SMTPResponder, _ *SMTPConnection, _ string) CommandResult {
	responder.Respond(&SMTPResponse{
		code:    250,
		message: "OK",
	})
	return CommandResultOK
}

func HandleQUIT(responder *SMTPResponder, _ *SMTPConnection, _ string) CommandResult {
	responder.Respond(&SMTPResponse{
		code:    221,
		message: "Service closing transmission channel",
	})
	return CommandResultDisconnect
}

func HandleRCPT(responder *SMTPResponder, connection *SMTPConnection, arguments string) CommandResult {
	if len(arguments) < 1 {
		responder.Respond(&SMTPResponse{
			code:    501,
			message: "Syntax: RCPT TO:<address>",
		})
		return CommandResultError
	}

	connection.message.to = append(connection.message.to, strings.TrimPrefix(arguments, "TO:"))
	responder.Respond(&SMTPResponse{
		code:    250,
		message: "OK",
	})
	return CommandResultOK
}

func HandleRSET(responder *SMTPResponder, connection *SMTPConnection, _ string) CommandResult {
	connection.message = SMTPMessage{}
	responder.Respond(&SMTPResponse{
		code:    250,
		message: "OK",
	})
	return CommandResultOK
}

func HandleSTARTTLS(responder *SMTPResponder, connection *SMTPConnection, _ string) CommandResult {
	tlsConfig := connection.context.Value(smtpContextKey("tlsConfig")).(*tls.Config)
	if tlsConfig == nil {
		responder.Respond(&SMTPResponse{
			code:    502,
			message: "Command not implemented",
		})
		return CommandResultError
	}

	responder.Respond(&SMTPResponse{
		code:    220,
		message: "Ready to start TLS",
	})
	responder.Flush()

	tlsConn := tls.Server(connection.netConnection, tlsConfig)

	err := tlsConn.Handshake()
	if err != nil {
		responder.Respond(&SMTPResponse{
			code:    550,
			message: "Failed to start TLS",
		})
		return CommandResultDisconnect
	}

	connection.netConnection = tlsConn
	connection.textConnection = textproto.NewConn(tlsConn)
	return CommandResultOK
}

func HandleUnknownCommand(responder *SMTPResponder, _ *SMTPConnection, _ string) CommandResult {
	responder.Respond(&SMTPResponse{
		code:    500,
		message: "Command not recognized",
	})
	return CommandResultError
}
