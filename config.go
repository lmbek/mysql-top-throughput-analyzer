package main

import (
	"flag"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config exposes configuration via getters/setters and hides implementation.
// Use LoadConfig() to obtain an instance.
type Config interface {
	// Getters
	DSN() string
	Interval() time.Duration
	ReadThreshold() uint64
	WriteThreshold() uint64
	AvgRowRead() uint64
	AvgRowSent() uint64
	TopN() int
	RealIO() bool
	MinPrintBytes() uint64
	// Rows-based thresholds (0 = disabled)
	ReadRowsThreshold() uint64
	WriteRowsThreshold() uint64
	MinPrintRows() uint64
	// Simple mode
	Simple() bool
	// Logging
	LogMode() string          // stdout|file|both
	LogFile() string          // path to logfile if file/both
	LogMaxSizeMB() int        // rotate when file grows past N MB
	LogMaxBackups() int       // keep at most N old files
	LogMaxAgeDays() int       // remove old files after N days
	LogCompress() bool        // gzip old files
	LogLevel() string         // INFO|WARN|ERROR|DEBUG

	// Setters
	SetDSN(string)
	SetInterval(time.Duration)
	SetReadThreshold(uint64)
	SetWriteThreshold(uint64)
	SetAvgRowRead(uint64)
	SetAvgRowSent(uint64)
	SetTopN(int)
	SetRealIO(bool)
	SetMinPrintBytes(uint64)
	SetReadRowsThreshold(uint64)
	SetWriteRowsThreshold(uint64)
	SetMinPrintRows(uint64)
	SetSimple(bool)
	SetLogMode(string)
	SetLogFile(string)
	SetLogMaxSizeMB(int)
	SetLogMaxBackups(int)
	SetLogMaxAgeDays(int)
	SetLogCompress(bool)
	SetLogLevel(string)
}

const (
	// Hardcoded defaults for local runs; can be overridden via flags or MON_* env vars.
	defaultDSN               = "app:apppass@tcp(127.0.0.1:3306)/?parseTime=true"
	defaultInterval          = 10 * time.Millisecond
	defaultThresholdStr      = "5MB" // legacy: if provided, applies to both read and write
	defaultReadThresholdStr  = "1MB" // preferred specific thresholds
	defaultWriteThresholdStr = "1MB"
	defaultAvgRowRead        = 200 // bytes per row examined
	defaultAvgRowSent        = 200 // bytes per row sent
	defaultTopN              = 5
	defaultRealIO            = true // enable engine I/O bytes monitoring by default
)

// config is the hidden implementation of Config.
type config struct {
	dsn            string
	interval       time.Duration
	readThreshold  uint64
	writeThreshold uint64
	avgRowRead     uint64
	avgRowSent     uint64
	topN           int
	realIO         bool
	minPrintBytes  uint64
	// Rows-based thresholds (0 = disabled)
	readRowsThreshold  uint64
	writeRowsThreshold uint64
	minPrintRows       uint64
	// Simple mode toggle
	simple bool
	// Logging
	logMode       string
	logFile       string
	logMaxSizeMB  int
	logMaxBackups int
	logMaxAgeDays int
	logCompress   bool
	logLevel      string
}

// LoadConfig parses flags and env vars, preserving precedence: flags > env > defaults
func LoadConfig() Config {
	var (
		dsn               string
		intervalStr       string
		thresholdStr      string // legacy combined threshold; if set explicitly it applies to both read/write
		readThresholdStr  string
		writeThresholdStr string
		minPrintStr       string
		avgRowRead        uint64
		avgRowSent        uint64
		topN              int
		realIO            bool
		// Rows-based thresholds (0 = disabled)
		readRowsThrStr  string
		writeRowsThrStr string
		minPrintRowsStr string
		// Simple mode toggle
		simpleMode        bool
)

	// If flags are not yet defined/parsed, define them. Otherwise, read from existing FlagSet.
	if !flag.Parsed() {
		if flag.Lookup("dsn") == nil {
			flag.StringVar(&dsn, "dsn", defaultDSN, "MySQL DSN (user:pass@tcp(host:port)/). Must have access to performance_schema.")
		}
		if flag.Lookup("interval") == nil {
			flag.StringVar(&intervalStr, "interval", defaultInterval.String(), "snapshot interval (e.g. 30s, 60s)")
		}
		if flag.Lookup("threshold") == nil {
			flag.StringVar(&thresholdStr, "threshold", defaultThresholdStr, "legacy: threshold bytes for alerting (applies to both read & write) (e.g. 5GB)")
		}
		if flag.Lookup("read-threshold") == nil {
			flag.StringVar(&readThresholdStr, "read-threshold", defaultReadThresholdStr, "min bytes read to consider high (e.g. 5GB)")
		}
		if flag.Lookup("write-threshold") == nil {
			flag.StringVar(&writeThresholdStr, "write-threshold", defaultWriteThresholdStr, "min bytes written to consider high (e.g. 5GB)")
		}
		if flag.Lookup("min-print-bytes") == nil {
			flag.StringVar(&minPrintStr, "min-print-bytes", "1MB", "minimum bytes to print offenders and engine I/O deltas (e.g. 1MB, 1MiB)")
		}
		if flag.Lookup("avg-read-bytes") == nil {
			flag.Uint64Var(&avgRowRead, "avg-read-bytes", defaultAvgRowRead, "assumed avg bytes per row examined")
		}
		if flag.Lookup("avg-sent-bytes") == nil {
			flag.Uint64Var(&avgRowSent, "avg-sent-bytes", defaultAvgRowSent, "assumed avg bytes per row sent")
		}
		if flag.Lookup("top") == nil {
			flag.IntVar(&topN, "top", defaultTopN, "print top N offenders each interval")
		}
		if flag.Lookup("real-io") == nil {
			flag.BoolVar(&realIO, "real-io", defaultRealIO, "use engine I/O bytes from InnoDB (SHOW GLOBAL STATUS) alongside digest estimates")
		}
		// Simple mode toggle
		if flag.Lookup("simple") == nil {
			flag.BoolVar(&simpleMode, "simple", false, "enable simple mode: only warn when a query's estimated bytes (read or write) exceed thresholds and log the full query sample")
		}
		// Rows-based flags (optional)
		if flag.Lookup("read-rows-threshold") == nil {
			flag.StringVar(&readRowsThrStr, "read-rows-threshold", "", "min rows examined to consider high (0 = disabled)")
		}
		if flag.Lookup("write-rows-threshold") == nil {
			flag.StringVar(&writeRowsThrStr, "write-rows-threshold", "", "min rows sent to consider high (0 = disabled)")
		}
		if flag.Lookup("min-print-rows") == nil {
			flag.StringVar(&minPrintRowsStr, "min-print-rows", "", "minimum rows to print offenders (0 = disabled)")
		}
		flag.Parse()
	} else {
		// Read from existing parsed flags if present
		if f := flag.Lookup("dsn"); f != nil { dsn = f.Value.String() }
		if f := flag.Lookup("interval"); f != nil { intervalStr = f.Value.String() }
		if f := flag.Lookup("threshold"); f != nil { thresholdStr = f.Value.String() }
		if f := flag.Lookup("read-threshold"); f != nil { readThresholdStr = f.Value.String() }
		if f := flag.Lookup("write-threshold"); f != nil { writeThresholdStr = f.Value.String() }
		if f := flag.Lookup("min-print-bytes"); f != nil { minPrintStr = f.Value.String() }
		if f := flag.Lookup("avg-read-bytes"); f != nil { if v, _ := strconv.ParseUint(f.Value.String(), 10, 64); v != 0 { avgRowRead = v } }
		if f := flag.Lookup("avg-sent-bytes"); f != nil { if v, _ := strconv.ParseUint(f.Value.String(), 10, 64); v != 0 { avgRowSent = v } }
		if f := flag.Lookup("top"); f != nil { if v, _ := strconv.Atoi(f.Value.String()); v != 0 { topN = v } }
		if f := flag.Lookup("real-io"); f != nil { lv := strings.ToLower(strings.TrimSpace(f.Value.String())); realIO = (lv == "1" || lv == "true" || lv == "yes" || lv == "on") }
		if f := flag.Lookup("simple"); f != nil { lv := strings.ToLower(strings.TrimSpace(f.Value.String())); simpleMode = (lv == "1" || lv == "true" || lv == "yes" || lv == "on") }
		if f := flag.Lookup("read-rows-threshold"); f != nil { readRowsThrStr = f.Value.String() }
		if f := flag.Lookup("write-rows-threshold"); f != nil { writeRowsThrStr = f.Value.String() }
		if f := flag.Lookup("min-print-rows"); f != nil { minPrintRowsStr = f.Value.String() }
	}

	setFlags := map[string]bool{}
	flag.CommandLine.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	if !setFlags["dsn"] {
		if v := os.Getenv("MON_DSN"); v != "" {
			dsn = v
		}
	}
	if !setFlags["interval"] {
		if v := os.Getenv("MON_INTERVAL"); v != "" {
			intervalStr = v
		}
	}
	if !setFlags["threshold"] {
		if v := os.Getenv("MON_THRESHOLD"); v != "" {
			thresholdStr = v
		}
	}
	if !setFlags["read-threshold"] {
		if v := os.Getenv("MON_READ_THRESHOLD"); v != "" {
			readThresholdStr = v
		}
	}
	if !setFlags["write-threshold"] {
		if v := os.Getenv("MON_WRITE_THRESHOLD"); v != "" {
			writeThresholdStr = v
		}
	}
	if !setFlags["min-print-bytes"] {
		if v := os.Getenv("MON_MIN_PRINT_BYTES"); v != "" {
			minPrintStr = v
		}
	}
	if !setFlags["avg-read-bytes"] {
		if v := os.Getenv("MON_AVG_READ_BYTES"); v != "" {
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				avgRowRead = n
			}
		}
	}
	if !setFlags["avg-sent-bytes"] {
		if v := os.Getenv("MON_AVG_SENT_BYTES"); v != "" {
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				avgRowSent = n
			}
		}
	}
	if !setFlags["top"] {
		if v := os.Getenv("MON_TOP"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				topN = n
			}
		}
	}
 if !setFlags["real-io"] {
		if v := os.Getenv("MON_REAL_IO"); v != "" {
			lv := strings.ToLower(strings.TrimSpace(v))
			realIO = (lv == "1" || lv == "true" || lv == "yes" || lv == "on")
		} else {
			realIO = defaultRealIO
		}
	}
	if !setFlags["simple"] {
		if v := os.Getenv("MON_SIMPLE"); v != "" {
			lv := strings.ToLower(strings.TrimSpace(v))
			simpleMode = (lv == "1" || lv == "true" || lv == "yes" || lv == "on")
		}
	}

	if dsn == "" {
		log.Fatal("dsn is required. Example: -dsn \"user:pass@tcp(127.0.0.1:3306)/\" or set MON_DSN env var")
	}
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		log.Fatalf("invalid interval: %v", err)
	}
	threshold, err := parseBytesFlag(thresholdStr)
	if err != nil {
		log.Fatalf("invalid threshold: %v", err)
	}

	var readThreshold, writeThreshold uint64
	if setFlags["threshold"] {
		readThreshold = threshold
		writeThreshold = threshold
	} else {
		if readThresholdStr == "" {
			readThresholdStr = defaultReadThresholdStr
		}
		if writeThresholdStr == "" {
			writeThresholdStr = defaultWriteThresholdStr
		}
		if v, err2 := parseBytesFlag(readThresholdStr); err2 == nil {
			readThreshold = v
		} else {
			log.Fatalf("invalid read-threshold: %v", err2)
		}
		if v, err2 := parseBytesFlag(writeThresholdStr); err2 == nil {
			writeThreshold = v
		} else {
			log.Fatalf("invalid write-threshold: %v", err2)
		}
	}

	// Parse min print bytes (suppression threshold for printing)
	minPrintBytes := uint64(1 << 20) // default 1 MiB
	if minPrintStr != "" {
		if v, err2 := parseBytesFlag(minPrintStr); err2 == nil {
			minPrintBytes = v
		} else {
			log.Fatalf("invalid min-print-bytes: %v", err2)
		}
	}

	// Rows-based thresholds via flags or env (strings may be empty => disabled)
	var readRowsThr, writeRowsThr, minPrintRows uint64
	// From flags strings if present
	if readRowsThrStr != "" {
		if v, err := strconv.ParseUint(strings.TrimSpace(readRowsThrStr), 10, 64); err == nil {
			readRowsThr = v
		} else {
			log.Fatalf("invalid read-rows-threshold: %v", err)
		}
	}
	if writeRowsThrStr != "" {
		if v, err := strconv.ParseUint(strings.TrimSpace(writeRowsThrStr), 10, 64); err == nil {
			writeRowsThr = v
		} else {
			log.Fatalf("invalid write-rows-threshold: %v", err)
		}
	}
	if minPrintRowsStr != "" {
		if v, err := strconv.ParseUint(strings.TrimSpace(minPrintRowsStr), 10, 64); err == nil {
			minPrintRows = v
		} else {
			log.Fatalf("invalid min-print-rows: %v", err)
		}
	}
	// Env overrides if flags not set
	if readRowsThr == 0 {
		if v := os.Getenv("MON_READ_ROWS_THRESHOLD"); v != "" {
			if n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64); err == nil { readRowsThr = n }
		}
	}
	if writeRowsThr == 0 {
		if v := os.Getenv("MON_WRITE_ROWS_THRESHOLD"); v != "" {
			if n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64); err == nil { writeRowsThr = n }
		}
	}
	if minPrintRows == 0 {
		if v := os.Getenv("MON_MIN_PRINT_ROWS"); v != "" {
			if n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64); err == nil { minPrintRows = n }
		}
	}

 return &config{
		dsn:                 dsn,
		interval:            interval,
		readThreshold:       readThreshold,
		writeThreshold:      writeThreshold,
		avgRowRead:          avgRowRead,
		avgRowSent:          avgRowSent,
		topN:                topN,
		realIO:              realIO,
		minPrintBytes:       minPrintBytes,
		readRowsThreshold:   readRowsThr,
		writeRowsThreshold:  writeRowsThr,
		minPrintRows:        minPrintRows,
		simple:              simpleMode,
		logMode:             coalesce(os.Getenv("MON_LOG_MODE"), "stdout"),
		logFile:             coalesce(os.Getenv("MON_LOG_FILE"), "/logs/monitor.jsonl"),
		logMaxSizeMB:        atoiDefault(os.Getenv("MON_LOG_MAX_SIZE_MB"), 50),
		logMaxBackups:       atoiDefault(os.Getenv("MON_LOG_MAX_BACKUPS"), 7),
		logMaxAgeDays:       atoiDefault(os.Getenv("MON_LOG_MAX_AGE_DAYS"), 14),
		logCompress:         boolEnv(os.Getenv("MON_LOG_COMPRESS"), true),
		logLevel:            strings.ToUpper(coalesce(os.Getenv("MON_LOG_LEVEL"), "INFO")),
	}
}

// Getters
func (c *config) DSN() string                 { return c.dsn }
func (c *config) Interval() time.Duration     { return c.interval }
func (c *config) ReadThreshold() uint64       { return c.readThreshold }
func (c *config) WriteThreshold() uint64      { return c.writeThreshold }
func (c *config) AvgRowRead() uint64          { return c.avgRowRead }
func (c *config) AvgRowSent() uint64          { return c.avgRowSent }
func (c *config) TopN() int                   { return c.topN }
func (c *config) RealIO() bool                { return c.realIO }
func (c *config) MinPrintBytes() uint64       { return c.minPrintBytes }
func (c *config) ReadRowsThreshold() uint64   { return c.readRowsThreshold }
func (c *config) WriteRowsThreshold() uint64  { return c.writeRowsThreshold }
func (c *config) MinPrintRows() uint64        { return c.minPrintRows }
func (c *config) Simple() bool                { return c.simple }
// Logging getters
func (c *config) LogMode() string       { return c.logMode }
func (c *config) LogFile() string       { return c.logFile }
func (c *config) LogMaxSizeMB() int     { return c.logMaxSizeMB }
func (c *config) LogMaxBackups() int    { return c.logMaxBackups }
func (c *config) LogMaxAgeDays() int    { return c.logMaxAgeDays }
func (c *config) LogCompress() bool     { return c.logCompress }
func (c *config) LogLevel() string      { return c.logLevel }

// Setters
func (c *config) SetDSN(v string)                  { c.dsn = v }
func (c *config) SetInterval(v time.Duration)      { c.interval = v }
func (c *config) SetReadThreshold(v uint64)        { c.readThreshold = v }
func (c *config) SetWriteThreshold(v uint64)       { c.writeThreshold = v }
func (c *config) SetAvgRowRead(v uint64)           { c.avgRowRead = v }
func (c *config) SetAvgRowSent(v uint64)           { c.avgRowSent = v }
func (c *config) SetTopN(v int)                    { c.topN = v }
func (c *config) SetRealIO(v bool)                 { c.realIO = v }
func (c *config) SetMinPrintBytes(v uint64)        { c.minPrintBytes = v }
func (c *config) SetReadRowsThreshold(v uint64)    { c.readRowsThreshold = v }
func (c *config) SetWriteRowsThreshold(v uint64)   { c.writeRowsThreshold = v }
func (c *config) SetMinPrintRows(v uint64)         { c.minPrintRows = v }
func (c *config) SetSimple(v bool)                 { c.simple = v }
// Logging setters
func (c *config) SetLogMode(v string)       { c.logMode = v }
func (c *config) SetLogFile(v string)       { c.logFile = v }
func (c *config) SetLogMaxSizeMB(v int)     { c.logMaxSizeMB = v }
func (c *config) SetLogMaxBackups(v int)    { c.logMaxBackups = v }
func (c *config) SetLogMaxAgeDays(v int)    { c.logMaxAgeDays = v }
func (c *config) SetLogCompress(v bool)     { c.logCompress = v }
func (c *config) SetLogLevel(v string)      { c.logLevel = v }

// helpers for env parsing
func coalesce(v, def string) string {
	v = strings.TrimSpace(v)
	if v == "" { return def }
	return v
}
func atoiDefault(v string, def int) int {
	v = strings.TrimSpace(v)
	if v == "" { return def }
	if n, err := strconv.Atoi(v); err == nil { return n }
	return def
}
func boolEnv(v string, def bool) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" { return def }
	return v == "1" || v == "true" || v == "yes" || v == "on"
}
