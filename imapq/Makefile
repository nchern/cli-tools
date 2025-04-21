NAME = imapq
OUT = bin/$(NAME)

.PHONY: install-deps
install-deps:
	@go mod download

.PHONY: lint
lint:
	@golint ./...

# collect all linters and checks eventually
.PHONY: check
check: lint
	@staticcheck ./...

.PHONY: vet
vet: check
	@go vet ./...

.PHONY: build
build: vet
	@go build -o $(OUT) .

.PHONY: install
install: build
	@go install ./...

# .PHONY: test
# test: vet
# 	# -race causes panics in BoltDB code
# 	go test -timeout=10s ./...
