# Database Top Throughput Analyzer

Monitors MySQL/MariaDB using performance_schema to find queries that cause high read/write throughput. Emits structured JSON logs (slog) with full query samples. This repository now includes a minimal, logs-only observability stack (Grafana + Loki + Promtail) — no metrics, no dashboards, just logs.

---

## Features
- Periodic snapshots of performance_schema.events_statements_summary_by_digest.
- Per-interval delta and estimated bytes:
  - read ≈ rows_examined × AvgRowRead
  - write ≈ rows_sent × AvgRowSent
- JSON logs via slog (machine-parsable) with full SQL sample when available.
- Engine I/O delta WARN logs that include the top related query sample when available.
- Alerts when either read OR write for a query ≥ MinPrintBytes; always include the sample.
- Optional write-heavy INSERT warning.
- Minimal logs pipeline: slog → Promtail → Loki → Grafana Explore.

Note: DIGEST_TEXT is normalized SQL (literals replaced). Estimates are heuristic; use to find outliers.

---

## Quick start

### View logs in Grafana (Loki) — zero config
- Start the stack: `docker compose up -d --build`
- Open Grafana: http://localhost:3000 (admin/admin)
- Go to Explore and select the default Loki datasource (already selected).
- Run one of these queries:
  - `{compose_service="monitor"} | json`
  - `{service="monitor"} | json`
- You should see JSON fields parsed (time, level, msg, service, digest, sample, read, write).

If you do not see logs:
- Check Promtail logs: `docker logs -f promtail`
- Check Loki readiness: http://localhost:3100/ready
- Check the monitor container is running and producing stdout: `docker logs -f monitor`
1) From the repo root:
   - docker compose up -d --build
   - This brings up: monitor, monitor-mysql, loki, promtail, grafana

2) Open Grafana (pre-provisioned Loki datasource, Explore ready):
   - http://localhost:3000 (admin / admin)
   - In Explore, run: {compose_service="monitor"} | json

3) Live logs directly from the app (SSE):
   - http://localhost:8088/logs

4) Alternatively, tail the container logs:
   - docker logs -f monitor

---

## Configuration
Set via environment variables (docker-compose already sets sensible defaults):
- MON_DSN: MySQL DSN. Example: app:apppass@tcp(mysql:3306)/appdb?parseTime=true
- MON_INTERVAL: Snapshot interval (e.g., 5s, 60s)
- MON_READ_THRESHOLD / MON_WRITE_THRESHOLD: Bytes thresholds for alerts
- MON_MIN_PRINT_BYTES: Minimum bytes to print offenders and engine I/O deltas
- MON_AVG_READ_BYTES / MON_AVG_SENT_BYTES: Avg bytes per examined/sent row
- MON_TOP: How many top offenders to print per interval

Docker Compose defaults are under services.monitor.environment and can be overridden via .env or your shell.

---

## What the logs look like (JSON)
- Engine I/O delta (WARN), with query context when available:
{"level":"WARN","msg":"engine io delta","interval":"1s","read":"0B","write":"2.51MiB","topDigest":"…","sample":"INSERT INTO `persons` ( NAME ) SELECT NAME FROM `persons`"}

- Snapshot header (INFO):
{"level":"INFO","msg":"snapshot","time":"2025-10-19T03:16:32+02:00","offenders_ge_1MiB":1,"topN":1}

- Offender line (INFO):
{"level":"INFO","msg":"offender","rank":1,"digest":"…","count":1,"bytesRead":"25.03MiB","bytesWrite":"0B","summary":"INSERT INTO `persons` ( NAME ) SELECT NAME FROM `persons`"}

- ALERT (WARN) when either read OR write ≥ threshold, always with sample:
{"level":"WARN","msg":"ALERT: thresholds exceeded","digest":"…","readThreshold":"1.00MiB","writeThreshold":"1.00MiB","actualRead":"0B","actualWrite":"2.51MiB","count":1,"sample":"INSERT INTO `persons` ( NAME ) SELECT NAME FROM `persons`"}

---

## Running with Docker Compose
The docker-compose.yml includes:
- monitor: The analyzer (this app)
- monitor-mysql: MySQL for the analyzer
- loki: Log store
- promtail: Collects Docker logs, parses slog JSON, ships to Loki
- grafana: UI for logs

Commands:
- Start all: docker compose up -d --build
- Stop and remove: docker compose down
- Tail monitor logs: docker logs -f monitor

You can still point MON_DSN to an external DB if desired; by default it connects to monitor-mysql.

---

## Logs pipeline (slog → Promtail → Loki → Grafana)
- monitor (slog JSON to stdout)
- Docker captures container stdout/stderr as JSON log files
- Promtail reads the Docker logs, parses the inner slog JSON, and ships to Loki
- Grafana reads logs from Loki (pre-provisioned "Loki" datasource, set as default)

Promtail stages (grafana/promtail-config.yml):
- docker stage: decodes Docker log envelope and exposes labels like compose_service
- json stage: parses slog fields such as time, level, msg, digest, sample, read, write
- labels stage: promotes level, digest, compose_service to Loki labels
- timestamp stage: uses the slog "time" (RFC3339Nano) as the log timestamp in Loki

Explore tips:
- {compose_service="monitor"} | json
- {compose_service="monitor", level="WARN"} | json
- {compose_service="monitor", digest="<digest>"} | json

If logs aren’t appearing in Grafana:
- Ensure Promtail has access to /var/lib/docker/containers and the Docker socket (see compose mounts)
- Check promtail logs: docker logs -f promtail
- Verify Loki is up: http://localhost:3100/ready
- Confirm the monitor is producing JSON logs: docker logs -f demo-monitor

---

## Development
- Build: go build -o monitor
- Run: go run .
- Lint/format: go fmt ./...

License: MIT

---

## HTTP streaming API (live logs in your browser)
The monitor exposes a long‑running Server‑Sent Events (SSE) endpoint that streams every slog JSON log line as it happens.

- Endpoint: http://localhost:8088/logs
- Protocol: text/event-stream (SSE)
- Behavior: No server timeout; stay connected to continuously receive events.

Examples:
- In a browser: open http://localhost:8088/logs and leave the tab open to watch live JSON log lines.
- With curl: curl -N http://localhost:8088/logs

Notes:
- Each event’s “data:” line is a single JSON object representing one slog entry (the same JSON written to stdout and shipped to Loki).
- The Docker Compose file exposes port 8088 from the monitor container.


---

## Super-simple Grafana setup (logs only)
This project already ships with a ready-to-run logs stack (Loki + Promtail + Grafana). Follow these steps and you’ll see your monitor logs in Grafana’s Explore in under 2 minutes.

1) Prerequisites
- Install Docker Desktop (Windows/macOS) or Docker Engine (Linux).
- Clone this repo and open a terminal in the project root.

2) Configure environment (optional)
- Copy .env.example to .env and adjust if needed:
  - Windows PowerShell: Copy-Item .env.example .env
  - macOS/Linux: cp .env.example .env
- The defaults work out of the box: the monitor connects to the bundled MySQL (monitor-mysql).

3) Start everything
- make up
  - or: docker compose up -d --build
- This brings up: monitor, monitor-mysql, loki, promtail, grafana.

4) Open Grafana and view logs
- Grafana: http://localhost:3000 (user: admin, pass: admin)
- Go to Explore, Loki is already the default datasource.
- Run either query:
  - {service="monitor"} | json
  - {compose_service="monitor"} | json
- You should now see your JSON slog lines with fields parsed (time, level, msg, service, digest, sample, read, write, etc.).

5) Live stream (direct from the app)
- Open http://localhost:8088/logs in your browser to watch a live Server-Sent Events stream of the same JSON logs.

Tips to generate activity
- Use the sample SQL in mysql/*.sql to produce read/write load after the containers are up. For example:
  - Windows PowerShell:
    - docker exec -i monitor-mysql mysql -uapp -papppass appdb < ./mysql/10mib-throughput-test-read.sql
    - docker exec -i monitor-mysql mysql -uapp -papppass appdb < ./mysql/10mib-throughput-test-write.sql
  - macOS/Linux:
    - docker exec -i monitor-mysql mysql -uapp -papppass appdb < ./mysql/10mib-throughput-test-read.sql
    - docker exec -i monitor-mysql mysql -uapp -papppass appdb < ./mysql/10mib-throughput-test-write.sql

Troubleshooting
- No logs in Grafana yet?
  - Check Promtail: docker logs -f promtail
  - Check Loki readiness: http://localhost:3100/ready
  - Check the monitor is running: docker logs -f monitor
  - Ensure you used the query: {service="monitor"} | json (exactly)
- Grafana login fails: restart the container or reset the admin password via env if you changed it.
- Reset the stack: docker compose down && (optional) delete contents of ./.docker to wipe stored data.

That’s it — no extra manual Grafana configuration is needed. Logs are already JSON via slog, Promtail parses and ships them to Loki, and Grafana is pre-wired to query Loki.

