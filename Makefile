run_api:
	go run ./app/services/chat-api/main.go

build_api:
	go build ./app/services/chat-api/.

tidy:
	go mod tidy

fmt:
	gofmt -s -w .