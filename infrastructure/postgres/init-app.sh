#!/bin/sh
set -eu
# The API role cannot create databases, roles, or objects in the public schema.
APP_PASSWORD="$(cat /run/secrets/db_app_password)"
export APP_PASSWORD
if [ "$DB_USER" = "$POSTGRES_USER" ]; then
  echo "Runtime and owner database roles must differ" >&2
  exit 1
fi
psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set=ON_ERROR_STOP=1 <<'SQL'
\getenv app_password APP_PASSWORD
\getenv app_user DB_USER
\getenv database_name POSTGRES_DB
CREATE ROLE :"app_user" LOGIN PASSWORD :'app_password' NOSUPERUSER NOCREATEDB NOCREATEROLE;
REVOKE ALL ON DATABASE :"database_name" FROM PUBLIC;
GRANT CONNECT ON DATABASE :"database_name" TO :"app_user";
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO :"app_user";
SQL
unset APP_PASSWORD
