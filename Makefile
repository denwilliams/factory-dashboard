BIN := factory-dashboard
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX ?= $(HOME)/.local

.PHONY: build install test vet demo clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) .

install: build
	install -d $(PREFIX)/bin
	install -m 0755 $(BIN) $(PREFIX)/bin/$(BIN)

test:
	go test ./...

vet:
	go vet ./...

# Runs against fake CLIs with made-up data in ./.demo — no accounts needed.
demo: build
	FACTORY_DASHBOARD_HOME=$(CURDIR)/.demo PATH="$(CURDIR)/scripts/demo:$$PATH" ./$(BIN) serve -port 7421

clean:
	rm -rf $(BIN) .demo
