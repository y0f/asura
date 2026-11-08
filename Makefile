BINARY   := asura
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -s -w -X main.version=$(VERSION)
export LDFLAGS
GOFLAGS  := -trimpath
# Keep distributed artifacts aligned with the reviewed runtime inventory.
# Ordinary developer targets remain configurable through GOFLAGS.
RELEASE_ENV := env GOTOOLCHAIN=local GOWORK=off GOFLAGS= GOEXPERIMENT= CGO_ENABLED=0 GOAMD64=v1 GOARM64=v8.0

TAILWIND := ./tailwindcss
TEMPL_VERSION = $(shell go list -m -f '{{.Version}}' github.com/a-h/templ)
TEMPL = go run github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION)

.PHONY: all build build-postgres css docs docs-check notices notices-check watch dev check test lint run clean hash-key release generate agent

all: build

generate:
	$(TEMPL) generate

docs:
	cd docs && JEKYLL_ENV=production bundle exec jekyll build --strict_front_matter --destination ../.build/docs

docs-check: docs
	go run ./cmd/docscheck -dir .build/docs

notices:
	go run ./cmd/notices

notices-check:
	$(RELEASE_ENV) go run ./cmd/notices -check -verify

css: generate
	$(TAILWIND) -i web/tailwind.input.css -o web/static/tailwind.css --minify

watch:
	$(TAILWIND) -i web/tailwind.input.css -o web/static/tailwind.css --watch

dev:
	bash dev.sh

build: css
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$$LDFLAGS" -o $(BINARY) ./cmd/asura

build-postgres: css
	CGO_ENABLED=0 go build $(GOFLAGS) -tags postgres -ldflags "$$LDFLAGS" -o $(BINARY)-postgres ./cmd/asura

test: generate
	go test -race -count=1 ./...

# Source checks use the committed assets and temporary test databases. Generate
# assets separately after changing templates or CSS. Documentation has its own
# optional Ruby toolchain and docs-check target.
check:
	go vet ./...
	go test ./...
	go build ./cmd/asura ./cmd/asura-agent

lint:
	go vet ./...

run: build
	./$(BINARY) -config config.yaml

clean:
	rm -f $(BINARY) $(BINARY)-postgres
	rm -rf dist

hash-key:
	@read -p "Enter API key: " key; \
	go run ./cmd/asura -hash-key "$$key"

agent:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$$LDFLAGS" -o asura-agent ./cmd/asura-agent

release: notices-check
	@mkdir -p dist
	$(RELEASE_ENV) GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$$LDFLAGS" -o dist/asura-linux-amd64 ./cmd/asura
	$(RELEASE_ENV) GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$$LDFLAGS" -o dist/asura-linux-arm64 ./cmd/asura
	$(RELEASE_ENV) GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$$LDFLAGS" -o dist/asura-agent-linux-amd64 ./cmd/asura-agent
	$(RELEASE_ENV) GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$$LDFLAGS" -o dist/asura-agent-linux-arm64 ./cmd/asura-agent
	cp LICENSE THIRD_PARTY_NOTICES.txt dist/
	cd dist && sha256sum asura-linux-amd64 asura-linux-arm64 asura-agent-linux-amd64 asura-agent-linux-arm64 LICENSE THIRD_PARTY_NOTICES.txt > checksums.txt
	@echo "Binaries, licenses and checksums written to dist/"
