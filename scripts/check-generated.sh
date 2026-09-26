#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

mkdir -p "$tmp/internal/api/generated" "$tmp/internal/db/generated"
cp api/openapi.yaml "$tmp/openapi.yaml"
cp api/codegen.yaml "$tmp/codegen.yaml"
cp sqlc.yaml "$tmp/sqlc.yaml"
cp -R db "$tmp/db"

(cd "$tmp" && "$root/.tools/bin/oapi-codegen" --config codegen.yaml openapi.yaml)
(cd "$tmp" && "$root/.tools/bin/sqlc" generate)

diff -ru internal/api/generated "$tmp/internal/api/generated"
diff -ru internal/db/generated "$tmp/internal/db/generated"
