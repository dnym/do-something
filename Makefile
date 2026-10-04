GO ?= go
VERSION ?= 0.1.0-dev
PREFIX ?= $(HOME)/.local
LDFLAGS = -s -w -X dosomething/internal/cli.Version=$(VERSION)

.PHONY: build install test check dist
build:
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/do-something ./cmd/dosomething
install: build
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 755 bin/do-something $(DESTDIR)$(PREFIX)/bin/do-something
test:
	$(GO) test ./...
check:
	$(GO) vet ./...
	$(GO) test -race ./...
	staticcheck ./...
	govulncheck ./...
dist:
	$(GO) run ./tools/dist -version '$(VERSION)'
