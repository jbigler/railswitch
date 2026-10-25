VERSION ?= dev

.PHONY: check build test

check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	staticcheck ./...
	go test ./...

test:
	go test ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-X github.com/jbigler/railswitch/internal/cli.Version=$(VERSION)" -o railswitch ./cmd/railswitch
