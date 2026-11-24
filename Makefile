BINARY := lazycast
ALIAS  := lc

.PHONY: build install test vet fmt clean

build:
	go build -o $(BINARY) ./cmd/lazycast

# Installs $(BINARY) to GOBIN (or GOPATH/bin) and symlinks a short
# alias next to it, since "lazycast" is a lot to type for something
# you'll run constantly.
install:
	go install ./cmd/lazycast
	ln -sf "$$(go env GOBIN || echo $$(go env GOPATH)/bin)/$(BINARY)" "$$(go env GOBIN || echo $$(go env GOPATH)/bin)/$(ALIAS)"

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

clean:
	rm -f $(BINARY)
