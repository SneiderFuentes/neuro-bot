.PHONY: build run stop dev test logs db-shell migrate-up migrate-down

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
