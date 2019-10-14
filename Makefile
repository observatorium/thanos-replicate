SRC = $(shell find . -type f -name '*.go' -not -path './vendor/*')

CONTAINER_CMD:=docker run --rm \
		-u="$(shell id -u):$(shell id -g)" \
		-v "$(shell go env GOCACHE):/.cache/go-build" \
		-v "$(PWD):/go/src/github.com/observatorium/thanos-replicate:Z" \
		-w "/go/src/github.com/observatorium/thanos-replicate" \
		-e USER=deadbeef \
		-e GO111MODULE=on \
		quay.io/coreos/jsonnet-ci

all: thanos-replicate

thanos-replicate: go-vendor $(SRC)
	CGO_ENABLED=0 GO111MODULE=on go build -mod vendor -v

.PHONY: go-vendor
go-vendor: go.mod go.sum
	go mod vendor

.PHONY: lint
lint:
	golangci-lint run -v --enable-all

.PHONY: test
test:
	CGO_ENABLED=1 GO111MODULE=on go test -v -race ./...

.PHONY: clean
clean:
	-rm thanos-replicate
