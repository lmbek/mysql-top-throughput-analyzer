-- Grant the monitor user access to performance_schema for monitoring
-- Runs only on first initialization of the data directory

-- Grant minimal privileges needed for monitoring performance_schema to the account created by entrypoint (app@'%')
GRANT SELECT ON performance_schema.* TO 'app'@'%';

-- Optionally, allow SHOW VIEW if needed for some metadata
GRANT SHOW VIEW ON *.* TO 'app'@'%';

FLUSH PRIVILEGES;
