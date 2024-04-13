


.PHONY: lint
lint:
	@golint ./...

.PHONY: vet
vet: lint
	 @go vet ./...

.PHONY: build
build:
	 @go build  ./...

.PHONY: test
test: vet
	@go test ./...

.PHONY: install
install:
	 @go install ./...
