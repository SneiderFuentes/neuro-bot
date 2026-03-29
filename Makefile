.PHONY: build run stop dev test test-cover test-race docker-test docker-test-cover docker-test-race logs db-shell migrate-up migrate-down

build:
	docker compose build bot

run:
	docker compose up -d

stop:
	docker compose down

dev:
	go run ./cmd/server

test:
	go test ./...

test-cover:
	go test -cover ./internal/...

test-race:
	go test -race ./...

docker-test:
	MSYS_NO_PATHCONV=1 docker run --rm -v "$$(pwd):/app" -w "/app" golang:1.23-alpine sh -c "go test ./... && echo OK"

docker-test-cover:
	MSYS_NO_PATHCONV=1 docker run --rm -v "$$(pwd):/app" -w "/app" golang:1.23-alpine sh -c "go test -cover ./internal/... && echo OK"

docker-test-race:
	MSYS_NO_PATHCONV=1 docker run --rm -v "$$(pwd):/app" -w "/app" golang:1.23 sh -c "go test -race ./... && echo OK"

migrate-up:
	docker compose exec bot ./neuro-bot migrate up

migrate-down:
	docker compose exec bot ./neuro-bot migrate down 1

logs:
	docker compose logs -f bot

db-shell:
	docker compose exec db mysql -u botuser -pbotpass neuro_bot

status:
	docker compose ps

stats:
	docker stats neuro_bot neuro_bot_db neuro_bot_ngrok
