GO ?= go
PREFIX ?= $(HOME)/.local
TEST_ARGS ?=

.PHONY: all build test race vet fmt install uninstall clean
all: build

build:
	CGO_ENABLED=1 $(GO) build -trimpath -o doubleshot .

test:
	$(GO) test -tags doubleshot_test -count=1 $(TEST_ARGS) ./...

race:
	CGO_ENABLED=1 $(GO) test -tags doubleshot_test -race -count=1 $(TEST_ARGS) ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

install: build
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 755 doubleshot "$(DESTDIR)$(PREFIX)/bin/doubleshot"

uninstall:
	rm -f "$(DESTDIR)$(PREFIX)/bin/doubleshot"

clean:
	rm -f doubleshot coverage.out
