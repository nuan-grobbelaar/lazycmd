BINARY := lazycmd
ALIAS  := lc

.PHONY: build install test vet fmt clean

build:
	go build -o $(BINARY) ./cmd/lazycmd

# Installs $(BINARY) to GOBIN (or GOPATH/bin) and symlinks a short
# alias next to it, since "lazycmd" is a lot to type for something
# you'll run constantly.
INSTALL_DIR := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)

install:
	go install ./cmd/lazycmd
	ln -sf $(BINARY) $(INSTALL_DIR)/$(ALIAS)
	@echo "installed $(INSTALL_DIR)/$(BINARY) and $(INSTALL_DIR)/$(ALIAS)"

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

clean:
	rm -f $(BINARY)
