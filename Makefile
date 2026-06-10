.PHONY: build test test-unit test-integration clean

BINARY := bin/rpu

build:
	go build -o $(BINARY) ./cmd/rpu

build-static-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY) ./cmd/rpu

test-unit:
	go test ./...

test-integration:
	cd test/integration && go test -tags=integration -v ./...

test: test-unit

clean:
	rm -f $(BINARY)
	go clean -testcache
