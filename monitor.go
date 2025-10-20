package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type Monitor interface{ Run(ctx context.Context) }

// offender represents a per-interval delta with estimated bytes
type offender struct {
	Digest       string
	Text         string
	BytesRead    uint64
	BytesWrite   uint64
	RowsExamined uint64
	RowsSent     uint64
	Count        uint64
}

type monitor struct {
	configuration Config
	db            DBClient
	reporter      Reporter
	log           *slog.Logger
}

func NewMonitor(configuration Config, db DBClient, r Reporter, log *slog.Logger) Monitor {
	return &monitor{configuration: configuration, db: db, reporter: r, log: log}
}

func (m *monitor) Run(ctx context.Context) {
	m.reporter.Startup(m.configuration)

	// test connect
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := m.db.Ping(pingCtx); err != nil {
		cancel()
		m.log.Error("ping db", "err", err)
		return
	}
	cancel()

	// initial snapshot
	prev, err := m.db.Snapshot(ctx)
	if err != nil {
		m.log.Error("initial snapshot failed", "err", err)
		return
	}

	// graceful shutdown signals
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(m.configuration.Interval())
	defer ticker.Stop()
	var mu sync.Mutex

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-stop:
			m.log.Info("shutting down")
			break loop
		case <-ticker.C:
			mu.Lock()
			curr, err := m.db.Snapshot(ctx)
			if err != nil {
				m.log.Error("fetch snapshot", "err", err)
				mu.Unlock()
				continue
			}

			delta := deltaSnap(prev, curr)
			for _, d := range delta {
				br := d.SumRowsExam * m.configuration.AvgRowRead()
				bw := d.SumRowsSent * m.configuration.AvgRowSent()
				if br == 0 && bw == 0 {
					continue
				}
				// Prefer real query sample when available (MySQL 8.0+), fall back to normalized DIGEST_TEXT
				text := d.DigestText
				if d.QuerySample.Valid && d.QuerySample.String != "" {
					text = d.QuerySample.String
				}
				o := offender{
					Digest:       d.Digest,
					Text:         text,
					BytesRead:    br,
					BytesWrite:   bw,
					RowsExamined: d.SumRowsExam,
					RowsSent:     d.SumRowsSent,
					Count:        d.CountStar,
				}
				if br >= m.configuration.ReadThreshold() || bw >= m.configuration.WriteThreshold() {
					m.reporter.Alert(o, m.configuration.ReadThreshold(), m.configuration.WriteThreshold())
				}
			}

			prev = curr
			mu.Unlock()
		}
	}

	if err := m.db.Close(); err != nil {
		m.log.Error("db close", "err", err)
	}
	m.reporter.Shutdown()
}
