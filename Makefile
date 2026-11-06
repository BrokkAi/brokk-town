BOTS := bug-bot feature-bot issue-bot review-bot release-bot simplifier-bot repo-bot mayor-bot
# Where make images tags Town's image and each bot's.
IMAGES ?= ghcr.io/brokkai/brokk-town
IMAGE_TAG ?= latest

.PHONY: build bots test js check images
build: bots
	go build -o bin/bt ./cmd/bt
bots:
	@for bot in $(BOTS); do go build -o bin/$$bot ./cmd/$$bot || exit 1; done
test:
	go test -race ./...
	$(MAKE) js
js:
	@for file in internal/web/*.js; do node --check "$$file" || exit 1; done
	node --test internal/web/*.test.js
check: test
	go vet ./...
# One image per bot plus Town's, all from the one Dockerfile.
images:
	@for target in bt $(BOTS); do docker build --target $$target -t $(IMAGES)/$$target:$(IMAGE_TAG) . || exit 1; done
