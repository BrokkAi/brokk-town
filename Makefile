.PHONY: build test check licenses smoke
build:
	python3 scripts/build_bundle.py
test:
	go test -race ./...
	@for project in bots/*; do (cd "$$project" && go test -race ./...) || exit; done
	npm test
licenses:
	python3 scripts/licenses.py
check: test licenses
	go vet ./...
	@for project in bots/*; do (cd "$$project" && go vet ./... && python3 scripts/licenses.py && python3 -m unittest discover -s scripts -p '*_test.py' && node --test --test-isolation=none npm/*.test.cjs) || exit; done
	sh -n install.sh
	node --test --test-isolation=none npm/bt.test.cjs
	npm run check
	python3 -m unittest discover -s scripts -p '*_test.py'

smoke: build
	python3 scripts/smoke.py
	python3 scripts/smoke_workers.py
