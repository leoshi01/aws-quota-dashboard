.PHONY: build run test clean docker

BINARY_NAME=aws-quota-dashboard
VERSION?=0.1.0

build:
	go build -o bin/$(BINARY_NAME) ./cmd/server

run:
	go run ./cmd/server

test:
	go test -v ./...

clean:
	rm -rf bin/

deps:
	go mod download
	go mod tidy

docker-build:
	docker build -t $(BINARY_NAME):$(VERSION) .

docker-run:
	docker run -p 8080:8080 \
		-e AWS_ACCESS_KEY_ID \
		-e AWS_SECRET_ACCESS_KEY \
		-e AWS_REGION \
		$(BINARY_NAME):$(VERSION)

lint:
	golangci-lint run

fmt:
	go fmt ./...
