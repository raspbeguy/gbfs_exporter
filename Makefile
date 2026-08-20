BINARY := gbfs_exporter

.PHONY: build test lint vet clean run

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) .

test:
	go test ./...

vet:
	go vet ./...

lint: vet
	@test -z "$$(gofmt -l .)" || (echo "gofmt found these files:"; gofmt -l .; exit 1)
	go mod tidy -diff

run: build
	./$(BINARY) -config config.yml

clean:
	rm -f $(BINARY)
