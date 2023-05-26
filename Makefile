run_api:
	go run app/services/chat-api/main.go

build_api:
	go build ${PWD}/app/services/chat-api/.

tidy:
	go mod tidy