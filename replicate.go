package main

import (
	"context"
	"math/rand"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/oklog/run"
	"github.com/oklog/ulid"
	opentracing "github.com/opentracing/opentracing-go"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/tsdb/labels"
	"github.com/thanos-io/thanos/pkg/extflag"
	"github.com/thanos-io/thanos/pkg/objstore/client"
	"github.com/thanos-io/thanos/pkg/runutil"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
)

const replicateComponent = "replicate"

func registerReplicate(m map[string]setupFunc, app *kingpin.Application, name string) {
	cmd := app.Command(name, "Runs replication as a long running daemon.")

	httpMetricsBindAddr := regHTTPAddrFlag(cmd)

	fromObjStoreConfig := regCommonObjStoreFlags(cmd, "from", false)
	toObjStoreConfig := regCommonObjStoreFlags(cmd, "to", false)

	matcherStrs := cmd.Flag("matcher", "Only blocks whose labels match this matcher will be replicated.").PlaceHolder("key=\"value\"").Strings()

	m[name] = func(g *run.Group, logger log.Logger, reg *prometheus.Registry, tracer opentracing.Tracer, _ bool) error {
		matchers, err := parseFlagMatchers(*matcherStrs)
		if err != nil {
			return errors.Wrap(err, "parse block label matchers")
		}

		return runReplicate(
			g,
			logger,
			reg,
			tracer,
			*httpMetricsBindAddr,
			labels.Selector(matchers),
			fromObjStoreConfig,
			toObjStoreConfig,
		)
	}
}

func runReplicate(
	g *run.Group,
	logger log.Logger,
	reg *prometheus.Registry,
	_ opentracing.Tracer,
	httpMetricsBindAddr string,
	labelSelector labels.Selector,
	fromObjStoreConfig *extflag.PathOrContent,
	toObjStoreConfig *extflag.PathOrContent,
) error {
	logger = log.With(logger, "component", "replicate")

	level.Debug(logger).Log("msg", "setting up metric http listen-group")

	if err := metricHTTPListenGroup(g, logger, reg, httpMetricsBindAddr); err != nil {
		return err
	}

	fromConfContentYaml, err := fromObjStoreConfig.Content()
	if err != nil {
		return err
	}

	if len(fromConfContentYaml) == 0 {
		return errors.New("No supported bucket was configured to replicate from")
	}

	fromReg := prometheus.WrapRegistererWith(prometheus.Labels{"replicate": "from"}, reg)
	fromBkt, err := client.NewBucket(logger, fromConfContentYaml, fromReg, replicateComponent)
	if err != nil {
		return err
	}

	toConfContentYaml, err := toObjStoreConfig.Content()
	if err != nil {
		return err
	}

	if len(toConfContentYaml) == 0 {
		return errors.New("No supported bucket was configured to replicate to")
	}

	toReg := prometheus.WrapRegistererWith(prometheus.Labels{"replicate": "to"}, reg)
	toBkt, err := client.NewBucket(logger, toConfContentYaml, toReg, replicateComponent)
	if err != nil {
		return err
	}

	replicationRunCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "thanos_replicate_replication_runs_total",
		Help: "The number of replication runs split by success and error.",
	}, []string{"result"})
	reg.MustRegister(replicationRunCounter)

	blockFilter := NewBlockFilter(logger, labelSelector).Filter
	metrics := newReplicationMetrics(reg)
	ctx, cancel := context.WithCancel(context.Background())

	g.Add(func() error {
		defer runutil.CloseWithLogOnErr(logger, fromBkt, "from bucket client")
		defer runutil.CloseWithLogOnErr(logger, toBkt, "to bucket client")

		return runutil.Repeat(time.Minute, ctx.Done(), func() error {
			timestamp := time.Now()
			entropy := ulid.Monotonic(rand.New(rand.NewSource(timestamp.UnixNano())), 0)
			ulid, err := ulid.New(ulid.Timestamp(timestamp), entropy)
			if err != nil {
				return errors.Wrap(err, "generate replication run-id")
			}
			logger := log.With(logger, "replication-run-id", ulid.String())

			level.Info(logger).Log("msg", "running replication attempt")
			err = newReplicationScheme(logger, metrics, blockFilter, fromBkt, toBkt).execute(ctx)
			if err != nil {
				level.Error(logger).Log("msg", "running replicaton failed", "err", err)
				replicationRunCounter.WithLabelValues("error").Inc()
				return nil
			}

			replicationRunCounter.WithLabelValues("success").Inc()
			level.Info(logger).Log("msg", "ran replication successfully")

			// No matter the error we want to repeat indefinitely.
			return nil
		})
	}, func(error) {
		cancel()
	})

	level.Info(logger).Log("msg", "starting replication")

	return nil
}
