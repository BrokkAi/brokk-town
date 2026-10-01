.PHONY: build test js check licenses smoke
build:
	python3 scripts/build.py
test:
	go test -race ./...
	$(MAKE) js
js:
	@for file in internal/web/*.js; do node --check "$$file" || exit 1; done
	node --test internal/web/*.test.js
licenses:
	python3 scripts/licenses.py
check: test licenses
	go vet ./...
	sh -n install.sh
	python3 -m unittest discover -s scripts -p '*_test.py'

smoke: build
	python3 scripts/smoke.py
	python3 scripts/smoke_mjolnir.py
