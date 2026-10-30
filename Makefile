VERSION ?= dev
# staticcheck runs at this version through `go run` (built once, then from
# Go's cache), so `make check` needs nothing on PATH beyond Go. Keep it in
# step with the workflows' install step.
STATICCHECK_VERSION = 2026.2.2

.PHONY: check build test

check:
	test -z "$$(gofmt -l cmd internal)"
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...
	go test ./...

test:
	go test ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-X github.com/jbigler/railswitch/internal/buildinfo.Version=$(VERSION)" -o railswitch ./cmd/railswitch
