-- Hydra keeps its own schema; created once by the postgres entrypoint.
SELECT 'CREATE DATABASE hydra' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'hydra')\gexec
