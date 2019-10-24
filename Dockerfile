FROM golang:1.13.1-alpine3.10 as builder

ADD . $GOPATH/src/github.com/observatorium/thanos-replicate
WORKDIR $GOPATH/src/github.com/observatorium/thanos-replicate

RUN apk update && apk upgrade && apk add --no-cache alpine-sdk

RUN git update-index --refresh; make thanos-replicate

# -----------------------------------------------------------------------------

FROM quay.io/prometheus/busybox:latest

COPY --from=builder /go/src/github.com/observatorium/thanos-replicate/thanos-replicate /bin/thanos-replicate

ENTRYPOINT [ "/bin/thanos-replicate" ]
