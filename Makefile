.PHONY: build test check licenses smoke
build:
	go build -o bin/bt ./cmd/bt
test:
	go test -race ./...
	npm test
licenses:
	python3 scripts/licenses.py
check: test licenses
	go vet ./...
	npm run check
	python3 -m unittest discover -s scripts -p '*_test.py'

smoke: build
	python3 scripts/smoke.py
