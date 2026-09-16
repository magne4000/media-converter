# media-converter build
#
# The binary is pure Go: CGO is disabled, so every target cross-compiles from
# any host with the Go toolchain alone. FFmpeg is located on the machine at
# runtime; see internal/app.

BIN         := media-converter
PKG         := ./cmd/media-converter
DIST        := dist
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO          ?= go
GOFLAGS     ?=
LDFLAGS     := -s -w -X main.version=$(VERSION)

# Every platform the binary is built for.
PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64 \
	windows/arm64

.PHONY: help
help:
	@echo "targets:"
	@echo "  build      build $(BIN) for the host platform"
	@echo "  all        build $(BIN) for every supported platform into $(DIST)/"
	@echo "  test       run the test suite"
	@echo "  check      gofmt and go vet"
	@echo "  checksums  write $(DIST)/SHA256SUMS for the built binaries"
	@echo "  install    go install into GOBIN"
	@echo "  clean      remove $(DIST)/"
	@echo ""
	@echo "supported platforms: $(PLATFORMS)"

.PHONY: build
build:
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

.PHONY: all
all: $(DIST)
	@set -e; for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		out=$(DIST)/$(BIN)-$$os-$$arch; \
		if [ "$$os" = "windows" ]; then out=$$out.exe; fi; \
		echo "building $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			$(GO) build $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" -o $$out $(PKG); \
	done

$(DIST):
	mkdir -p $(DIST)

.PHONY: checksums
checksums: all
	cd $(DIST) && shasum -a 256 $(BIN)-* > SHA256SUMS
	@cat $(DIST)/SHA256SUMS

.PHONY: test
test:
	$(GO) test ./...

.PHONY: check
check:
	@unformatted=$$(gofmt -l ./cmd ./internal); \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	$(GO) vet ./...

.PHONY: install
install:
	CGO_ENABLED=0 $(GO) install $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" $(PKG)

.PHONY: clean
clean:
	rm -rf $(DIST) $(BIN)
