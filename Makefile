BINARY_SERVER=ds3sql-server
BINARY_CLI=ds3sql
GOBUILD=go build
GOTEST=go test
GOVET=go vet
GOFMT=gofmt -l -s

.PHONY: all build build-server build-cli test vet fmt clean run

all: build

build: build-server build-cli

build-server:
	$(GOBUILD) -o $(BINARY_SERVER) ./cmd/ds3sql-server/

build-cli:
	$(GOBUILD) -o $(BINARY_CLI) ./cmd/ds3sql/

test:
	$(GOTEST) -v -race ./...

vet:
	$(GOVET) ./...

fmt:
	$(GOFMT) ./

clean:
	rm -f $(BINARY_SERVER) $(BINARY_CLI)
	rm -rf tmp/

run: build-server
	./$(BINARY_SERVER)
