# monitor/Dockerfile
# Multi-stage: build static binary, then run in a tiny image
FROM golang:1.25 AS build

WORKDIR /app
# Copy your Go module files first to leverage caching
COPY go.mod go.sum ./
RUN go mod download

# Copy full source (all packages)
COPY . .

# Ensure module files are tidy and sums complete after copying the full source
RUN go mod tidy
RUN go mod download

# Build static binary for Linux AMD64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/monitor_queries .

# ---

# Use a minimal Alpine runtime image instead of distroless Debian
FROM alpine:3.20

# Install runtime dependencies (CA certs, tzdata) and create non-root user
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app && adduser -S -G app appuser

WORKDIR /app
COPY --from=build /out/monitor_queries /app/monitor_queries

# Ensure binary is executable
RUN chmod +x /app/monitor_queries

# Drop privileges
USER appuser

# All runtime configuration is provided via docker-compose (environment section) or .env
# No default ENV values are set in the image to avoid conflicts.

ENTRYPOINT ["/app/monitor_queries"]
