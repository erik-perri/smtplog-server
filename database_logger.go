package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/oklog/ulid/v2"
	"log"
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

	stmtIns, err := logger.pool.Prepare(
		"INSERT INTO smtp_connection_logs (ulid, remote_address, remote_port, created_at, updated_at) " +
			"values (?, ?, ?, NOW(), NOW())",
	)
	if err != nil {
		return 0, err
	}

	defer func(stmtIns *sql.Stmt) {
		err := stmtIns.Close()
		if err != nil {
			log.Printf("Failed to close statement %s", err)
		}
	}(stmtIns)

	result, err := stmtIns.Exec(ulid.Make().String(), remoteAddress, remotePort)
	if err != nil {
		return 0, err
	}

	return result.LastInsertId()
}

func (logger *DatabaseLogger) LogMessage(
	connectionID int64,
	direction LogDirection,
	data []byte,
) (int64, error) {
	_, cancel := context.WithTimeout(logger.context, 5*time.Second)
	defer cancel()

	stmtIns, err := logger.pool.Prepare(
		"INSERT INTO smtp_message_logs (ulid, smtp_connection_log_id, direction, data, created_at, updated_at)" +
			" values (?, ?, ?, ?, NOW(), NOW())",
	)
	if err != nil {
		return 0, err
	}

	defer func(stmtIns *sql.Stmt) {
		err := stmtIns.Close()
		if err != nil {
			log.Printf("Failed to close statement %s", err)
		}
	}(stmtIns)

	result, err := stmtIns.Exec(ulid.Make().String(), connectionID, direction, data)
	if err != nil {
		return 0, err
	}

	return result.LastInsertId()
}

func (logger *DatabaseLogger) Close() error {
	return logger.pool.Close()
}
