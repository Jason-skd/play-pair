GOOSE_VERSION := v3.28.0
SQLC_VERSION := v1.31.1
OAPI_VERSION := v2.8.0
TOOLS := $(CURDIR)/.tools/bin
DB_URL ?= postgres://playpair:playpair_dev@127.0.0.1:55432/playpair?sslmode=disable

.PHONY: tools db-up db-down migrate-up generate run check test-integration test-smoke

tools: $(TOOLS)/goose $(TOOLS)/sqlc $(TOOLS)/oapi-codegen

$(TOOLS)/goose:
	GOBIN=$(TOOLS) go install -tags='no_clickhouse no_mssql no_mysql no_sqlite3 no_ydb no_vertica no_turso' github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)

$(TOOLS)/sqlc:
	GOBIN=$(TOOLS) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

$(TOOLS)/oapi-codegen:
	GOBIN=$(TOOLS) go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_VERSION)

db-up:
	docker compose up -d --wait postgres

db-down:
	docker compose down

migrate-up: $(TOOLS)/goose
	$(TOOLS)/goose -dir db/migrations postgres '$(DB_URL)' up

generate: tools
	$(TOOLS)/oapi-codegen --config api/codegen.yaml api/openapi.yaml
	$(TOOLS)/sqlc generate

run:
	DATABASE_URL='$(DB_URL)' go run ./cmd/playpair-api

check: tools
	test -z "$$(gofmt -l cmd internal)"
	go vet ./...
	go build ./...
	go test ./...
	sh scripts/check-generated.sh

test-integration: $(TOOLS)/goose
	: "$${TEST_DATABASE_URL:?TEST_DATABASE_URL is required}"
	$(TOOLS)/goose -dir db/migrations postgres "$$TEST_DATABASE_URL" up
	PLAYPAIR_INTEGRATION=1 go test -count=1 ./internal/db ./internal/app

test-smoke:
	sh scripts/test-smoke.sh
