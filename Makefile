# Simple Makefile for building, testing, and running the monitor
# Usage examples:
#   make build
#   make run ARGS="-dsn 'user:pass@tcp(127.0.0.1:3306)/' -interval 5s"
#   make test
#   make cover
#   make cover-html
#   make docker-up
#   make docker-down

APP := monitor_queries
PKG := ./...
COVER := coverage.out

# Load .env variables
ifneq (,$(wildcard .env))
	include .env
	export
endif

.PHONY: all build clean run test cover cover-html fmt vet docker-up docker-down docker-restart

all: build

build:
	go build -o $(APP) .

clean:
	rm -f $(APP) $(COVER) coverage.html

run:
	go run . $(ARGS)

fmt:
	go fmt $(PKG)

vet:
	go vet $(PKG)

test:
	go test -race -count=1 $(PKG)

cover:
	go test -race -covermode=atomic -coverprofile=$(COVER) $(PKG)
	go tool cover -func=$(COVER)

cover-html: cover
	go tool cover -html=$(COVER) -o coverage.html

# Docker Compose helpers (requires Docker)
up:
	docker compose up -d --build

down:
	docker compose down

d-restart:
	docker compose down || true
	docker compose up -d --build

status:
	docker ps -a

prune:
	docker system prune

# Bring up full observability stack and print access URLs
observe:
	#@echo "Starting monitor + Loki + Promtail + Prometheus + Grafana..."
	#docker compose up -d --build
	@echo "Grafana:   http://localhost:3000 (admin/admin)"
	@echo "Loki:      http://localhost:3100/ready"
	@echo "SSE logs:  http://localhost:8088/logs"
