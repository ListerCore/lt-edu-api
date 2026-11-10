APP__DB__USER := joseph
APP__DB__PASSWORD := joseph_dev
APP__DB__NAME := joseph_db
APP__DB__CONTAINER := joseph-db
APP__REDIS__CONTAINER := joseph-redis
APP__HOST__PORT := 5050
APP__GRPC__PORT := 5051
APP__NETWORK__NAME := joseph-net

ifeq ($(OS),Windows_NT)
    NULL_DEVICE := NUL
    AIR_CONFIG  := .air.windows.toml
else
    NULL_DEVICE := /dev/null
    AIR_CONFIG  := .air.toml
endif

.PHONY: dev dev/run build test lint fmt vet health proto grpc/health grpc/list live/ping \
        network/create \
        up setup down \
        db/start redis/start db/stop redis/stop db/psql redis/cli \
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

# Regenerates internal/adapter/inbound/grpc/gen from proto/ (needs buf, protoc-gen-go and protoc-gen-go-grpc)
proto:
	buf lint
	buf generate

# Needs grpcurl; reflection is on outside production
grpc/list:
	grpcurl -plaintext localhost:$(APP__GRPC__PORT) list

grpc/health:
	grpcurl -plaintext -d '{"include_db": true, "include_redis": true}' localhost:$(APP__GRPC__PORT) joseph.system.v1.SystemService/GetHealth

# Publishes a test event on the live stream; open pages receive it through /ws/live
live/ping:
	docker exec $(APP__REDIS__CONTAINER) redis-cli XADD joseph:live MAXLEN '~' 1000 '*' type system.ping data '{}'

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

up: network/create
	docker compose up -d --wait db redis
	@echo "Infra lista — corré 'make dev' para levantar la API con live reload."

setup: network/create
	docker compose up -d --wait db redis

down:
	docker compose down -v

logs:
	docker compose logs -f db redis
