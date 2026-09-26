#!/bin/sh
set -eu

: "${TEST_DATABASE_URL:?TEST_DATABASE_URL is required}"
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
server_pid=
cleanup() {
  if [ -n "$server_pid" ]; then
    kill -TERM "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT HUP INT TERM

cd "$root"
go build -o "$tmp/playpair-api" ./cmd/playpair-api
port=18080
while curl -fsS --max-time 1 "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; do
  port=$((port + 1))
done
DATABASE_URL="$TEST_DATABASE_URL" HTTP_ADDR="127.0.0.1:$port" "$tmp/playpair-api" >"$tmp/server.log" 2>&1 &
server_pid=$!

attempt=0
until curl -fsS --max-time 1 "http://127.0.0.1:$port/readyz" >"$tmp/response" 2>/dev/null; do
  if ! kill -0 "$server_pid" 2>/dev/null; then cat "$tmp/server.log"; exit 1; fi
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 100 ]; then cat "$tmp/server.log"; exit 1; fi
  sleep 0.1
done

kill -TERM "$server_pid"
wait "$server_pid"
server_pid=
if curl -fsS --max-time 1 "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; then
  echo "server still listening after SIGTERM" >&2
  exit 1
fi
