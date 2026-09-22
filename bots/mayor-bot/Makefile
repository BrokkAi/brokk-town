.PHONY: build test check
build:
	go build -o bin/bmb ./cmd/bmb
test:
	go test -race ./...
check: test
	go vet ./...
