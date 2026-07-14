.PHONY: build run test vet fmt lint clean

build:
	go build $(if $(TAGS),-tags $(TAGS)) -o dirtree ./cmd/dirtree

run: build
	./dirtree $(ARGS)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

lint:
	golangci-lint run

clean:
	rm -f dirtree
