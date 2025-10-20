package main

import (
	"context"
	"database/sql"
	_ "github.com/go-sql-driver/mysql"
)

// DBClient abstracts DB operations needed by the monitor (slimmed)
// 1) Interface
// 2) (no file-level constants)
// 3) Struct (unexported)
// 4) Constructor returns interface with pointer to struct

type DBClient interface {
	Ping(ctx context.Context) error
	Snapshot(ctx context.Context) (snapshot, error)
	Close() error
}

// DB-related internal types (moved from types.go)
// They live here because they are produced by the DB layer.

type snapKey string

type digestStat struct {
	Digest      string
	DigestText  string
	QuerySample sql.NullString // real sample SQL if available (MySQL 8.0+: QUERY_SAMPLE_TEXT)
	CountStar   uint64
	SumRowsExam uint64
	SumRowsSent uint64
	SumRowsAff  uint64 // rows affected (INSERT/UPDATE/DELETE/REPLACE)
}

type snapshot map[snapKey]digestStat

// mysqlClient is the hidden implementation of DBClient

type mysqlClient struct{ db *sql.DB }

// NewMySQLClient constructs a DBClient backed by MySQL
func NewMySQLClient(dsn string) (DBClient, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	// Ensure only one physical connection used
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	return &mysqlClient{db: db}, nil
}

func (c *mysqlClient) Close() error { return c.db.Close() }

func (c *mysqlClient) Ping(ctx context.Context) error { return c.db.PingContext(ctx) }

func (c *mysqlClient) Snapshot(ctx context.Context) (snapshot, error) {
	const q = `
SELECT DIGEST, DIGEST_TEXT, QUERY_SAMPLE_TEXT, COUNT_STAR, SUM_ROWS_EXAMINED, SUM_ROWS_SENT, SUM_ROWS_AFFECTED
FROM performance_schema.events_statements_summary_by_digest
WHERE DIGEST IS NOT NULL`
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	snap := make(snapshot)
	for rows.Next() {
		var d digestStat
		if err := rows.Scan(&d.Digest, &d.DigestText, &d.QuerySample, &d.CountStar, &d.SumRowsExam, &d.SumRowsSent, &d.SumRowsAff); err != nil {
			return nil, err
		}
		snap[snapKey(d.Digest)] = d
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return snap, nil
}

// EngineIO removed in slimmed build
