module github.com/observatorium/thanos-replicate

go 1.13

require (
	github.com/go-kit/kit v0.9.0
	github.com/oklog/run v1.0.0
	github.com/oklog/ulid v1.3.1
	github.com/opentracing/opentracing-go v1.1.0
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v1.2.1
	github.com/prometheus/common v0.7.0
	github.com/prometheus/prometheus v1.8.2-0.20190913102521-8ab628b35467
	github.com/prometheus/tsdb v0.10.0
	github.com/thanos-io/thanos v0.8.1
	go.uber.org/automaxprocs v1.2.0
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
)
