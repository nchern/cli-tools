NAME = mailexpire

.PHONY: test build install
test:
	go vet ./...
	go test -race -timeout=60s ./...

build: test
	go build -o bin/$(NAME) .

install: test
	go install .
