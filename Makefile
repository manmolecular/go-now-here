build:
	docker compose build

run:
	docker compose up -d api

up: run

stop:
	docker compose stop api

delete: stop
	-docker rm go-now-here-api
	-docker rmi go-now-here/api

prune: delete

run_api:
	go run ./app/services/api/main.go

build_api:
	go build -o api ./app/services/api/.

tidy:
	go mod tidy

vendor: tidy
	rm -rf vendor
	go mod vendor

fmt:
	gofmt -s -w .
