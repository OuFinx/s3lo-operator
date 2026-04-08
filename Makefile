.PHONY: build test clean docker

BINARY := s3lo-proxy
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/s3lo-proxy

test:
	go test ./... -v

clean:
	rm -f $(BINARY)

docker:
	docker build -t s3lo-operator:$(VERSION) .
