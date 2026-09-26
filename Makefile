VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build run test lang release

build:
	go build -ldflags "$(LDFLAGS)" -o pureblog-go ./cmd/pureblog-go

run: build
	mkdir -p site && ./pureblog-go -dir site -addr :8080

test:
	go vet ./...
	go test -race ./...

# Re-generate web/lang/*.json from a checkout of the PHP version.
lang:
	go run ./tools/langconv -src ../pureblog/lang -dst web/lang

release:
	mkdir -p dist
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/pureblog-go-linux-amd64 ./cmd/pureblog-go
	GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/pureblog-go-linux-arm64 ./cmd/pureblog-go
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/pureblog-go-darwin-amd64 ./cmd/pureblog-go
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/pureblog-go-darwin-arm64 ./cmd/pureblog-go
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/pureblog-go-windows-amd64.exe ./cmd/pureblog-go
