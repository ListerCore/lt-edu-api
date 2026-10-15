APP__DB__USER := joseph
APP__DB__PASSWORD := joseph_dev
APP__DB__NAME := joseph_db
APP__DB__CONTAINER := joseph-db
APP__REDIS__CONTAINER := joseph-redis
APP__HOST__PORT := 8080
APP__NETWORK__NAME := joseph-net

ifeq ($(OS),Windows_NT)
    NULL_DEVICE := NUL
    AIR_CONFIG  := .air.windows.toml
else
    NULL_DEVICE := /dev/null
    AIR_CONFIG  := .air.toml
endif

.PHONY: dev dev/run build test lint fmt vet health \
        network/create \
        up setup down \
        db/start redis/start db/stop redis/stop db/psql redis/cli \
        migrate/up migrate/down migrate/adopt \
        logs

dev:
	@command -v air > $(NULL_DEVICE) 2>&1 && air -c $(AIR_CONFIG) || ( \
		echo "air no está instalado — corriendo sin live reload."; \
		echo "Instalalo con: go install github.com/air-verse/air@latest"; \
		$(MAKE) dev/run \
	)

dev/run:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server
	go build -o bin/migrate ./cmd/migrations

test:
	go test ./...

vet:
	go vet ./...

lint: vet
	gofmt -l .

fmt:
	gofmt -w .

health:
	curl -s "http://localhost:$(APP__HOST__PORT)/api/v1/health?db=true&redis=true" | python3 -m json.tool

network/create:
	-@docker network create --driver bridge $(APP__NETWORK__NAME) > $(NULL_DEVICE) 2>&1 || true
	@echo "Network $(APP__NETWORK__NAME) ready"

db/start: network/create
	docker compose up -d --wait db

db/stop:
	docker compose stop db

db/psql:
	docker exec -it $(APP__DB__CONTAINER) psql -U $(APP__DB__USER) -d $(APP__DB__NAME)

redis/start: network/create
	docker compose up -d --wait redis

redis/stop:
	docker compose stop redis

redis/cli:
	docker exec -it $(APP__REDIS__CONTAINER) redis-cli

migrate/up:
	go run ./cmd/migrations up

migrate/down:
	go run ./cmd/migrations down

migrate/adopt:
	go run ./cmd/migrations adopt

up: network/create
	docker compose up -d --wait db redis
	$(MAKE) migrate/up
	@echo "Infra lista — corré 'make dev' para levantar la API con live reload."

setup: network/create
	docker compose up -d --wait db redis
	$(MAKE) migrate/up

down:
	docker compose down -v

logs:
	docker compose logs -f db redis
