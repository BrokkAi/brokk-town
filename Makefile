.PHONY: build test js check
build:
	go build -o bin/sct ./cmd/sct
test:
	go test -race ./...
	$(MAKE) js
js:
	@for file in internal/web/*.js; do node --check "$$file" || exit 1; done
	node --test internal/web/*.test.js
check: test
	go vet ./...
