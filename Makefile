build:
	docker-compose build

run:
	docker-compose up -d api

up: run

stop:
	docker-compose stop api

delete: stop
	docker rm go-now-here-api

run_api:
	go run ./app/services/chat-api/main.go

build_api:
	go build ./app/services/chat-api/.

tidy:
	go mod tidy

fmt:
	gofmt -s -w .