# Developer shortcuts. CI runs the same commands; see .github/workflows/.

GO ?= go
GORELEASER ?= $(GO) run github.com/goreleaser/goreleaser/v2@latest

.PHONY: build test vet fmt-check check release-check release-snapshot

build:
	$(GO) build ./...

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt-check:
	@files="$$(gofmt -l .)"; if [ -n "$$files" ]; then echo "gofmt drift in:"; echo "$$files"; exit 1; fi

check: build fmt-check vet test

# Validate .goreleaser.yaml against the GoReleaser v2 schema. Exits 2 with
# "valid, but uses deprecated properties" because of the `brews` section; see
# the note at the top of .goreleaser.yaml.
release-check:
	$(GORELEASER) check

# Build every release artifact into dist/ without a tag, a GitHub token, or
# any upload. Useful before cutting a tag.
release-snapshot:
	$(GORELEASER) release --snapshot --clean --skip=publish
