BINARY   := asura
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -s -w -X main.version=$(VERSION)
export LDFLAGS
GOFLAGS  := -trimpath

TAILWIND := ./tailwindcss
TEMPL_VERSION = $(shell go list -m -f '{{.Version}}' github.com/a-h/templ)
TEMPL = go run github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION)

.PHONY: all build build-postgres css docs docs-check watch dev check test lint run clean hash-key release generate agent

all: build

generate:
	$(TEMPL) generate

docs:
	cd docs && JEKYLL_ENV=production bundle exec jekyll build --strict_front_matter --destination ../.build/docs

docs-check: docs
	go run ./cmd/docscheck -dir .build/docs

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

release:
	@mkdir -p dist
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$$LDFLAGS" -o dist/asura-linux-amd64 ./cmd/asura
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$$LDFLAGS" -o dist/asura-linux-arm64 ./cmd/asura
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$$LDFLAGS" -o dist/asura-agent-linux-amd64 ./cmd/asura-agent
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$$LDFLAGS" -o dist/asura-agent-linux-arm64 ./cmd/asura-agent
	cd dist && sha256sum asura-linux-amd64 asura-linux-arm64 asura-agent-linux-amd64 asura-agent-linux-arm64 > checksums.txt
	@echo "Binaries written to dist/"
