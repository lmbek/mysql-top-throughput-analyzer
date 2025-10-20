# mysql/Dockerfile
FROM mysql:8.0

# Enable performance_schema + a few safe defaults for observability
# Use a filename that sorts last to ensure our settings take precedence
COPY config.cnf /etc/mysql/conf.d/zz-observe.cnf

# Safety: strip any lingering deprecated/invalid options from base image layers if present
RUN set -eux; \
    find /etc/mysql -type f -name '*.cnf' -print0 | xargs -0 -I{} sh -c "sed -i '/^performance_schema_statements_digest_long_text\s*=\s*/d' '{}' || true"; \
    true

# Optional: create a mount point for init scripts if you want seed SQL
# (compose mounts ./mysql/initdb.d -> /docker-entrypoint-initdb.d)
