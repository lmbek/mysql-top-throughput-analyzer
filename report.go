package main

import (
	"log/slog"
)

// Reporter abstracts how results are reported (slimmed)
type Reporter interface {
	Startup(configuration Config)
	Alert(o offender, readThreshold, writeThreshold uint64) // always logs full sample
	Shutdown()
}

// logReporter hides implementation behind Reporter
type logReporter struct{ log *slog.Logger }

// NewReporter hides the concrete reporter implementation behind the Reporter interface
func NewReporter(log *slog.Logger) Reporter { return &logReporter{log: log} }

func (r *logReporter) Startup(cfg Config) {
	r.log.Info("starting monitor",
		"interval", cfg.Interval().String(),
		"readThreshold", bytesToHuman(cfg.ReadThreshold()),
		"writeThreshold", bytesToHuman(cfg.WriteThreshold()),
		"avgRowRead", cfg.AvgRowRead(),
		"avgRowSent", cfg.AvgRowSent(),
	)
}

func (r *logReporter) Alert(o offender, readThreshold, writeThreshold uint64) {
	r.log.Warn("ALERT: thresholds exceeded",
		"digest", o.Digest,
		"readThreshold", bytesToHuman(readThreshold),
		"writeThreshold", bytesToHuman(writeThreshold),
		"actualRead", bytesToHuman(o.BytesRead),
		"actualWrite", bytesToHuman(o.BytesWrite),
		"actualRowsExamined", o.RowsExamined,
		"actualRowsSent", o.RowsSent,
		"count", o.Count,
		"sample", o.Text, // full, untrimmed sample
	)
}

func (r *logReporter) Shutdown() { r.log.Info("monitor stopped") }
