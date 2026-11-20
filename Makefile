APP__DB__USER := ltedu
APP__DB__PASSWORD := ltedu_dev
APP__DB__NAME := ltedu_db
APP__DB__CONTAINER := lt-edu-db
APP__REDIS__CONTAINER := lt-edu-redis
APP__HOST__PORT := 5050
APP__GRPC__PORT := 5051
APP__NETWORK__NAME := lt-edu-net

ifeq ($(OS),Windows_NT)
    NULL_DEVICE := NUL
    AIR_CONFIG  := .air.windows.toml
else
    NULL_DEVICE := /dev/null
    AIR_CONFIG  := .air.toml
endif

.PHONY: dev dev/run build test lint fmt vet health proto grpc/health grpc/list live/ping \
        migrate/up migrate/down migrate/version migrate/create seed \
        network/create \
        up setup down \
        db/start redis/start livekit/start db/stop redis/stop livekit/stop db/psql redis/cli \
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
	go build -o bin/migrate ./cmd/migrate
	go build -o bin/seed ./cmd/seed

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

# Database schema (migrations/*.sql, embedded in the binary)
migrate/up:
	go run ./cmd/migrate up

migrate/down:
	go run ./cmd/migrate down

migrate/version:
	go run ./cmd/migrate version

# Datos iniciales (migrations/seed, cada archivo una sola vez) y el superadmin de APP__ADMIN__*
seed:
	go run ./cmd/seed

# make migrate/create name=create_institutions
migrate/create:
	go run ./cmd/migrate create $(name)

# Regenerates internal/adapter/inbound/grpc/gen from proto/ (needs buf, protoc-gen-go and protoc-gen-go-grpc)
proto:
	buf lint
	buf generate

# Needs grpcurl; reflection is on outside production
grpc/list:
	grpcurl -plaintext localhost:$(APP__GRPC__PORT) list

grpc/health:
	grpcurl -plaintext -d '{"include_db": true, "include_redis": true}' localhost:$(APP__GRPC__PORT) ltedu.system.v1.SystemService/GetHealth

# Publishes a test event on the live stream; open pages receive it through /ws/live
live/ping:
	docker exec $(APP__REDIS__CONTAINER) redis-cli XADD lt-edu:live MAXLEN '~' 1000 '*' type system.ping data '{}'

network/create:
	-@docker network create --driver bridge $(APP__NETWORK__NAME) > $(NULL_DEVICE) 2>&1 || true
	@echo "Network $(APP__NETWORK__NAME) ready"

db/start: network/create
	docker compose up -d --wait db

db/stop:
	docker compose stop db

db/psql:
	docker exec -it $(APP__DB__CONTAINER) psql -U $(APP__DB__USER) -d $(APP__DB__NAME)

# Respaldo de la base (scripts/backup.sh) y prueba de que se restaura (scripts/restore-check.sh <archivo>)
db/backup:
	DB_CONTAINER=$(APP__DB__CONTAINER) APP__DB__USER=$(APP__DB__USER) APP__DB__NAME=$(APP__DB__NAME) ./scripts/backup.sh

db/restore-check:
	DB_CONTAINER=$(APP__DB__CONTAINER) APP__DB__USER=$(APP__DB__USER) APP__DB__NAME=$(APP__DB__NAME) ./scripts/restore-check.sh $(DUMP)

# Sesiones de prueba por rol de una institución demo (recorrido y prueba de carga); demo/sessions-cleanup las borra
demo/sessions:
	go run ./cmd/demosessions -slug $(or $(SLUG),demo-eba)

demo/sessions-cleanup:
	go run ./cmd/demosessions -cleanup

redis/start: network/create
	docker compose up -d --wait redis

redis/stop:
	docker compose stop redis

# LiveKit (video calls inside the platform) in dev mode: ws://localhost:7880, key devkey, secret secret
livekit/start: network/create
	docker compose up -d livekit

livekit/stop:
	docker compose stop livekit

redis/cli:
	docker exec -it $(APP__REDIS__CONTAINER) redis-cli

# Infra + esquema + datos iniciales; idempotente, se puede correr las veces que haga falta
up: network/create
	docker compose up -d --wait db redis
	go run ./cmd/migrate up
	go run ./cmd/seed
	@echo "Infra lista — corré 'make dev' para levantar la API con live reload."

setup: network/create
	docker compose up -d --wait db redis
	go run ./cmd/migrate up
	go run ./cmd/seed
	@echo "Setup listo: base migrada y con datos iniciales."

down:
	docker compose down -v

logs:
	docker compose logs -f db redis
