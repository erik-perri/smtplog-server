package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
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

type AuthenticationMechanism int

const (
	AuthenticationMechanismNone  AuthenticationMechanism = 0
	AuthenticationMechanismLogin AuthenticationMechanism = 1
	AuthenticationMechanismPlain AuthenticationMechanism = 2
)

type SMTPMessage struct {
	data string
	from string
	to   []string
}

type SMTPConnection struct {
	authMechanism   AuthenticationMechanism
	authLines       []string
	cancel          context.CancelFunc
	context         context.Context
	connectionID    int64
	isDisconnecting bool
	isReadingData   bool
	isReadingAuth   bool
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

		output := fmt.Sprintf("%d%s%s", message.code, separator, message.message)

		log.Printf("> %s", output)

		err = connection.writeOutput(output)

		// Since 221 is the response to a quit command, we don't want to log it as an error
		// in case it was just a client that closed the connection before reading.
		if err != nil && message.code != 221 {
			log.Printf("Failed to send %d to %s", message.code, connection.netConnection.RemoteAddr())
		}
	}
}

func (n *SMTPMessage) HasRecipient(address string) bool {
	for _, to := range n.to {
		if to == address {
			return true
		}
	}
	return false
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

func (n *SMTPConnection) writeOutput(output string) error {
	n.logMessage(LogDirectionOut, []byte(output))

	return n.textConnection.PrintfLine(output)
}

func (n *SMTPConnection) readInput() (string, error) {
	err := n.netConnection.SetReadDeadline(time.Now().Add(
		time.Duration(n.context.Value(smtpContextKey("readTimeout")).(int)) * time.Second,
	))
	if err != nil {
		return "", err
	}

	input, err := n.textConnection.ReadLine()

	if err == nil {
		n.logMessage(LogDirectionIn, []byte(input))
	}

	return input, err
}

func (n *SMTPConnection) logMessage(
	direction LogDirection,
	data []byte,
) {
	_, err := n.context.Value(smtpContextKey("logger")).(*DatabaseLogger).LogMessage(n.connectionID, direction, data)
	if err != nil {
		log.Printf("Failed to log message, %s", err)
	}
}

func (n *SMTPConnection) logMail(message SMTPMessage) {
	_, err := n.context.Value(smtpContextKey("logger")).(*DatabaseLogger).LogMail(n.connectionID, message)
	if err != nil {
		log.Printf("Failed to log mail, %s", err)
	}
}

func (n *SMTPConnection) WaitForCommands() {
	for {
		select {
		case <-n.context.Done():
			return
		default:
			netData, err := n.readInput()
			if err != nil {
				if !n.canRetryError(err) {
					return
				}
				continue
			}

			if n.HandleCommand(netData) == CommandResultDisconnect {
				return
			}
		}
	}
}

func (n *SMTPConnection) canRetryError(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Temporary() {
		if !opErr.Timeout() {
			log.Printf("Failed to read from connection, %s", err)
			time.Sleep(time.Millisecond * 100)
		}

		if n.isDisconnecting {
			return false
		}

		return true
	}

	return false
}

func (n *SMTPConnection) HandleCommand(input string) CommandResult {
	log.Printf("< %s", input)

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

	if n.isReadingAuth {
		return HandleAuthPayload(&responder, n, input)
	}

	smtpCommands := map[string]func(*SMTPResponder, *SMTPConnection, string) CommandResult{
		"AUTH":     handleAUTH,
		"DATA":     handleDATA,
		"EHLO":     handleEHLO,
		"HELO":     handleHELO,
		"HELP":     handleHELP,
		"MAIL":     handleMAIL,
		"NOOP":     handleNOOP,
		"QUIT":     handleQUIT,
		"RCPT":     handleRCPT,
		"RSET":     handleRSET,
		"STARTTLS": handleSTARTTLS,
		"VRFY":     handleVRFY,
	}

	if smtpCommands[command] == nil {
		return handleUnknownCommand(&responder, n, input)
	}

	return smtpCommands[command](&responder, n, arguments)
}

func HandleAuthPayload(responder *SMTPResponder, connection *SMTPConnection, input string) CommandResult {
	connection.isReadingAuth = false

	switch connection.authMechanism {
	case AuthenticationMechanismNone:
	case AuthenticationMechanismPlain:
		break
	case AuthenticationMechanismLogin:
		connection.authLines = append(connection.authLines, input)

		if len(connection.authLines) < 2 {
			responder.Respond(&SMTPResponse{
				code:    334,
				message: base64.StdEncoding.EncodeToString([]byte("Password:")),
			})
			connection.isReadingAuth = true
		} else {
			responder.Respond(&SMTPResponse{
				code:    235,
				message: "2.7.0 Authentication successful",
			})
		}

		return CommandResultOK
	}

	return CommandResultError
}

func handleAUTH(responder *SMTPResponder, connection *SMTPConnection, input string) CommandResult {
	parts := strings.SplitN(input, " ", 2)
	if len(parts) == 1 {
		parts = append(parts, "")
	}

	mechanism, arguments := parts[0], parts[1]

	authHandlers := map[string]func(*SMTPResponder, *SMTPConnection, string) CommandResult{
		"LOGIN": handleAuthLOGIN,
		"PLAIN": handleAuthPLAIN,
	}

	if authHandlers[mechanism] == nil {
		return handleUnknownCommand(responder, connection, arguments)
	}

	return authHandlers[mechanism](responder, connection, arguments)
}

func handleAuthPLAIN(responder *SMTPResponder, connection *SMTPConnection, arguments string) CommandResult {
	connection.authMechanism = AuthenticationMechanismPlain
	connection.authLines = []string{arguments}

	responder.Respond(&SMTPResponse{
		code:    235,
		message: "2.7.0 Authentication successful",
	})

	return CommandResultOK
}

func handleAuthLOGIN(responder *SMTPResponder, connection *SMTPConnection, _ string) CommandResult {
	connection.authMechanism = AuthenticationMechanismLogin
	connection.authLines = []string{}
	connection.isReadingAuth = true

	responder.Respond(&SMTPResponse{
		code:    334,
		message: base64.StdEncoding.EncodeToString([]byte("Username:")),
	})

	return CommandResultOK
}

func handleDATA(responder *SMTPResponder, connection *SMTPConnection, _ string) CommandResult {
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

		connection.logMail(connection.message)
		connection.message = SMTPMessage{}

		responder.Respond(&SMTPResponse{
			code:    250,
			message: "OK",
		})
	} else {
		connection.message.data += strings.TrimPrefix(input, ".") + "\n"
	}
	return CommandResultOK
}

func handleEHLO(responder *SMTPResponder, connection *SMTPConnection, _ string) CommandResult {
	lines := []string{
		connection.context.Value(smtpContextKey("bannerHost")).(string),
		"PIPELINING",
	}

	authTypes := []string{
		"LOGIN",
		"PLAIN",
	}
	authLine := fmt.Sprintf("AUTH %s", strings.Join(authTypes, " "))

	lines = append(lines, authLine)

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

func handleHELO(responder *SMTPResponder, connection *SMTPConnection, _ string) CommandResult {
	responder.Respond(&SMTPResponse{
		code:    250,
		message: connection.context.Value(smtpContextKey("bannerHost")).(string),
	})
	return CommandResultOK
}

func handleHELP(responder *SMTPResponder, _ *SMTPConnection, _ string) CommandResult {
	responder.Respond(&SMTPResponse{
		code:    214,
		message: "I'm sorry Dave, I'm afraid I can't do that",
	})
	return CommandResultOK
}

func handleMAIL(responder *SMTPResponder, connection *SMTPConnection, arguments string) CommandResult {
	if len(arguments) < 1 || !strings.HasPrefix(arguments, "FROM:") {
		responder.Respond(&SMTPResponse{
			code:    501,
			message: "Syntax: MAIL FROM:<address>",
		})
		return CommandResultError
	}

	arguments = strings.TrimPrefix(arguments, "FROM:")
	address, _, err := splitAddressCommand(arguments)
	if err != nil {
		responder.Respond(&SMTPResponse{
			code:    501,
			message: "Syntax: MAIL FROM:<address>",
		})
		return CommandResultError
	}

	connection.message.from = address
	responder.Respond(&SMTPResponse{
		code:    250,
		message: "OK",
	})
	return CommandResultOK
}

func handleNOOP(responder *SMTPResponder, _ *SMTPConnection, _ string) CommandResult {
	responder.Respond(&SMTPResponse{
		code:    250,
		message: "OK",
	})
	return CommandResultOK
}

func handleQUIT(responder *SMTPResponder, _ *SMTPConnection, _ string) CommandResult {
	responder.Respond(&SMTPResponse{
		code:    221,
		message: "Service closing transmission channel",
	})
	return CommandResultDisconnect
}

func handleRCPT(responder *SMTPResponder, connection *SMTPConnection, arguments string) CommandResult {
	if len(arguments) < 1 || !strings.HasPrefix(arguments, "TO:") {
		responder.Respond(&SMTPResponse{
			code:    501,
			message: "Syntax: RCPT TO:<address>",
		})
		return CommandResultError
	}

	arguments = strings.TrimPrefix(arguments, "TO:")
	address, _, err := splitAddressCommand(arguments)
	if err != nil {
		responder.Respond(&SMTPResponse{
			code:    501,
			message: "Syntax: RCPT TO:<address>",
		})
		return CommandResultError
	}

	if !connection.message.HasRecipient(address) {
		connection.message.to = append(connection.message.to, address)
	}

	responder.Respond(&SMTPResponse{
		code:    250,
		message: "OK",
	})
	return CommandResultOK
}

func handleRSET(responder *SMTPResponder, connection *SMTPConnection, _ string) CommandResult {
	connection.message = SMTPMessage{}
	responder.Respond(&SMTPResponse{
		code:    250,
		message: "OK",
	})
	return CommandResultOK
}

func handleSTARTTLS(responder *SMTPResponder, connection *SMTPConnection, _ string) CommandResult {
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

func handleUnknownCommand(responder *SMTPResponder, _ *SMTPConnection, _ string) CommandResult {
	responder.Respond(&SMTPResponse{
		code:    500,
		message: "Command not recognized",
	})
	return CommandResultError
}

func handleVRFY(responder *SMTPResponder, _ *SMTPConnection, _ string) CommandResult {
	responder.Respond(&SMTPResponse{
		code:    252,
		message: "Cannot VRFY",
	})
	return CommandResultOK
}

func splitAddressCommand(arguments string) (string, string, error) {
	if len(arguments) < 1 || !strings.HasPrefix(arguments, "<") || !strings.HasSuffix(arguments, ">") {
		return "", "", fmt.Errorf("invalid address")
	}

	arguments = strings.TrimPrefix(arguments, "<")
	parts := strings.SplitN(arguments, ">", 2)
	if len(parts) != 2 {
		parts = append(parts, "")
	}

	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}
