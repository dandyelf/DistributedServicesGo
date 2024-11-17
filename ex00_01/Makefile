.DEFAULT_GOAL := build

.PHONY: fmt vet build lint docker-up docker-down clean

fmt:
	go fmt ./...

vet: fmt
	go vet ./...

build: vet
	go build ./cmd/server
	go build ./cmd/client

lint:
	golangci-lint run

docker-up:
	docker compose up -d

docker-down:
	docker compose down

clean:
	rm -rf server client
