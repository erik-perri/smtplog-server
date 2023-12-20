package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/oklog/ulid/v2"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type DatabaseLogger struct {
	context context.Context
	pool    *sql.DB
}

type LogDirection int

const (
	LogDirectionIn  LogDirection = 0
	LogDirectionOut LogDirection = 1
)

type RecipientType int

const (
	RecipientFrom RecipientType = 0
	RecipientTo   RecipientType = 1
)

func CreateDatabaseLogger(ctx context.Context, dataSourceName string) (*DatabaseLogger, error) {
	parts := strings.SplitN(dataSourceName, "://", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid dsn, expected format driver://user:password@host:port/database")
	}

	driverName := parts[0]
	dataSourceName = parts[1]

	pool, err := sql.Open(driverName, dataSourceName)
	if err != nil {
		return nil, err
	}

	pool.SetConnMaxLifetime(time.Minute * 5)
	pool.SetMaxOpenConns(10)
	pool.SetMaxIdleConns(10)

	err = pool.Ping()
	if err != nil {
		return nil, err
	}

	return &DatabaseLogger{
		context: ctx,
		pool:    pool,
	}, nil
}

func (logger *DatabaseLogger) LogConnection(
	remoteAddress string,
	remotePort int,
) (int64, error) {
	_, cancel := context.WithTimeout(logger.context, 5*time.Second)
	defer cancel()

	stmtInsert, err := logger.pool.Prepare(
		"INSERT INTO connections (ulid, remote_address, remote_port, created_at, updated_at) " +
			"values (?, ?, ?, NOW(), NOW())",
	)
	if err != nil {
		return 0, err
	}

	result, err := stmtInsert.Exec(
		strings.ToLower(ulid.Make().String()),
		remoteAddress,
		remotePort,
	)
	if err != nil {
		return 0, err
	}

	_ = stmtInsert.Close()

	return result.LastInsertId()
}

func (logger *DatabaseLogger) LogMessage(
	connectionID int64,
	direction LogDirection,
	data []byte,
) (int64, error) {
	_, cancel := context.WithTimeout(logger.context, 5*time.Second)
	defer cancel()

	stmtInsert, err := logger.pool.Prepare(
		"INSERT INTO connection_messages (ulid, connection_id, direction, data, created_at, updated_at)" +
			" values (?, ?, ?, ?, NOW(), NOW())",
	)
	if err != nil {
		return 0, err
	}

	result, err := stmtInsert.Exec(
		strings.ToLower(ulid.Make().String()),
		connectionID,
		direction,
		data,
	)
	if err != nil {
		return 0, err
	}

	_ = stmtInsert.Close()

	return result.LastInsertId()
}

func (logger *DatabaseLogger) fetchOrCreateRecipient(address string) (int64, error) {
	_, cancel := context.WithTimeout(logger.context, 5*time.Second)
	defer cancel()

	stmtSelect, err := logger.pool.Prepare(
		"SELECT id FROM recipients WHERE email = ?",
	)
	if err != nil {
		return 0, err
	}

	var existingID int64
	err = stmtSelect.QueryRow(address).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	if existingID != 0 {
		return existingID, nil
	}

	stmtInsert, err := logger.pool.Prepare(
		"INSERT INTO recipients (email, created_at, updated_at) values (?, NOW(), NOW())",
	)
	if err != nil {
		return 0, err
	}

	result, err := stmtInsert.Exec(address)
	if err != nil {
		return 0, err
	}

	_ = stmtInsert.Close()

	return result.LastInsertId()
}

func (logger *DatabaseLogger) LogMail(
	connectionID int64,
	message SMTPMessage,
) (int64, error) {
	_, cancel := context.WithTimeout(logger.context, 5*time.Second)
	defer cancel()

	mailID, err := logger.createMail(connectionID, message.data)
	if err != nil {
		return 0, err
	}

	fromRecipientID, err := logger.fetchOrCreateRecipient(message.from)
	if err != nil {
		return mailID, err
	}

	_, err = logger.createMailRecipient(mailID, fromRecipientID, RecipientTo)
	if err != nil {
		return mailID, err
	}

	for _, to := range message.to {
		toRecipientID, err := logger.fetchOrCreateRecipient(to)
		if err != nil {
			return mailID, err
		}
		_, err = logger.createMailRecipient(mailID, toRecipientID, RecipientFrom)
		if err != nil {
			return mailID, err
		}
	}

	return mailID, nil
}

func (logger *DatabaseLogger) Close() error {
	return logger.pool.Close()
}

func (logger *DatabaseLogger) createMail(connectionID int64, data string) (int64, error) {
	_, cancel := context.WithTimeout(logger.context, 5*time.Second)
	defer cancel()

	stmtInsert, err := logger.pool.Prepare(
		"INSERT INTO mail (ulid, connection_id, data, created_at, updated_at)" +
			" values (?, ?, ?, NOW(), NOW())",
	)
	if err != nil {
		return 0, err
	}

	result, err := stmtInsert.Exec(
		strings.ToLower(ulid.Make().String()),
		connectionID,
		data,
	)
	if err != nil {
		return 0, err
	}

	_ = stmtInsert.Close()

	return result.LastInsertId()
}

func (logger *DatabaseLogger) createMailRecipient(
	mailID int64,
	recipientID int64,
	recipientType RecipientType,
) (int64, error) {
	_, cancel := context.WithTimeout(logger.context, 5*time.Second)
	defer cancel()

	stmtInsert, err := logger.pool.Prepare(
		"INSERT INTO mail_recipient (mail_id, recipient_id, type, created_at, updated_at)" +
			" values (?, ?, ?, NOW(), NOW())",
	)
	if err != nil {
		return 0, err
	}

	result, err := stmtInsert.Exec(mailID, recipientID, recipientType)
	if err != nil {
		return 0, err
	}

	_ = stmtInsert.Close()

	return result.LastInsertId()
}
