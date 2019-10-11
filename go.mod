module github.com/observatorium/thanos-replicate

go 1.12

replace github.com/thanos-io/thanos => github.com/thanos-io/thanos v0.0.0-20190910123208-19e59ef5369c

require (
	github.com/go-kit/kit v0.9.0
	github.com/oklog/run v1.0.0
	github.com/oklog/ulid v1.3.1
	github.com/opentracing/opentracing-go v1.1.0
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v1.1.0
	github.com/prometheus/common v0.7.0
	github.com/prometheus/prometheus v1.8.2-0.20190913102521-8ab628b35467
	github.com/thanos-io/thanos v0.0.0-00010101000000-000000000000
	go.uber.org/automaxprocs v1.2.0
	google.golang.org/grpc v1.24.0 // indirect
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
)
