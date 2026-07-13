.PHONY: build run test vet fmt clean

build:
	go build -o dirtree ./cmd/dirtree

run: build
	./dirtree $(ARGS)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

clean:
	rm -f dirtree
