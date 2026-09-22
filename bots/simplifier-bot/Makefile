.PHONY: build test check
build:
	go build -o bin/bsb ./cmd/bsb
test:
	go test -race ./...
check: test
	go vet ./...
