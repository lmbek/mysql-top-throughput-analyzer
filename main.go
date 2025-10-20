// monitor_queries.go
//
// Overvåger MySQL/MariaDB ved at tage snapshots af performance_schema.events_statements_summary_by_digest
// Bruger kun én DB-forbindelse. Estimerer bytes read/write vha. rows_examined / rows_sent * avgRowBytes.
// Alarmerer (stdout) hvis nogen query overstiger threshold i snapshot-interval.
//
// Build: go build -o monitor_queries monitor_queries.go
// Run:   ./monitor_queries -dsn "user:pass@tcp(db-host:3306)/" -interval 60s -threshold 5GB
//
// Krav:
// - performance_schema skal være slået til.
// - DB-brugeren skal kunne SELECT fra performance_schema.events_statements_summary_by_digest

package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	// Load configuration first so logging can honor level/mode settings
	configuration := LoadConfig()

	// Determine log level from config (default INFO)
	lvl := slog.LevelInfo
	switch strings.ToUpper(configuration.LogLevel()) {
	case "DEBUG":
		lvl = slog.LevelDebug
	case "INFO":
		lvl = slog.LevelInfo
	case "WARN", "WARNING":
		lvl = slog.LevelWarn
	case "ERROR":
		lvl = slog.LevelError
	}

	// Set up broadcaster and tee writer to mirror slog JSON lines to SSE (stdout only)
	broadcaster := NewLogStreamBroadcaster()
	tee := NewLogTeeWriter(os.Stdout, broadcaster)

	// Configure slog JSON for Loki-friendly fields and stable formatting
	// - Keep time key as "time" with RFC3339Nano (default)
	// - Ensure level is an uppercase string
	// - Add constant attributes for easier querying in Loki/Grafana
	handler := slog.NewJSONHandler(tee, &slog.HandlerOptions{
		Level:     lvl,
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Force level to uppercase text
			if a.Key == slog.LevelKey {
				if lv, ok := a.Value.Any().(slog.Level); ok {
					name := lv.String()
					return slog.String(slog.LevelKey, strings.ToUpper(name))
				}
			}
			return a
		},
	})

	host, _ := os.Hostname()
	logger := slog.New(handler).With(
		"service", "monitor",
		"host", host,
		"pid", os.Getpid(),
	)

	// HTTP server: frontpage and SSE logs
	mux := http.NewServeMux()
	// Frontpage for /
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body><h1>Go to <a href=\"/logs\">/logs</a></h1></body></html>"))
	})
	// SSE endpoints for /logs and /logs/
	mux.HandleFunc("/logs", LogsSSEHandler(broadcaster, logger))
	mux.HandleFunc("/logs/", LogsSSEHandler(broadcaster, logger))

	srv := &http.Server{Addr: ":8088", Handler: mux}
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("http server starting", "addr", srv.Addr, "endpoint", "/logs")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	client, err := NewMySQLClient(configuration.DSN())
	if err != nil {
		logger.Error("open db", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := client.Close(); err != nil {
			logger.Error("db close", "err", err)
		}
	}()

	reporter := NewReporter(logger)
	mon := NewMonitor(configuration, client, reporter, logger)
	ctx := context.Background()
	mon.Run(ctx)

	// Graceful shutdown of HTTP server after monitor stops
	shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shCtx); err != nil {
		logger.Error("http server shutdown", "err", err)
	} else {
		logger.Info("http server stopped")
	}

	select {
	case err := <-serverErr:
		logger.Error("http server error", "err", err)
	default:
	}
}
