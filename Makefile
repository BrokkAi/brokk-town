.PHONY: build test check licenses smoke
build:
	python3 scripts/build.py
test:
	go test -race ./...
	npm test
licenses:
	python3 scripts/licenses.py
check: test licenses
	go vet ./...
	sh -n install.sh
	node --test --test-isolation=none npm/bt.test.cjs
	npm run check
	python3 -m unittest discover -s scripts -p '*_test.py'

smoke: build
	python3 scripts/smoke.py
	python3 scripts/smoke_mjolnir.py
