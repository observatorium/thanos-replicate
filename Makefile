SRC = $(shell find . -type f -name '*.go' -not -path './vendor/*')

all: thanos-replicate

tmp/help.txt: thanos-replicate
	mkdir -p tmp
	./thanos-replicate run --help &> tmp/help.txt

README.md: tmp/help.txt
	embedmd -w README.md

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
