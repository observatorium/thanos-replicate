package main

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/oklog/run"
	"github.com/oklog/ulid"
	opentracing "github.com/opentracing/opentracing-go"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/tsdb/labels"
	"github.com/thanos-io/thanos/pkg/compact"
	"github.com/thanos-io/thanos/pkg/compact/downsample"
	"github.com/thanos-io/thanos/pkg/extflag"
	"github.com/thanos-io/thanos/pkg/objstore/client"
	"github.com/thanos-io/thanos/pkg/runutil"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
)

const replicateComponent = "replicate"

// TODO(bwplotka): Consider moving to Thanos. Consider adding --labels to add to meta.json if empty to match shipper
// logic: https://github.com/observatorium/thanos-replicate/issues/7.
func registerReplicate(m map[string]setupFunc, app *kingpin.Application, name string) {
	cmd := app.Command(name, "Runs replication as a long running daemon.")

	httpMetricsBindAddr := regHTTPAddrFlag(cmd)

	// TODO(bwplotka): Add support for local filesystem bucket implementation.
	fromObjStoreConfig := regCommonObjStoreFlags(cmd, "from", false)
	toObjStoreConfig := regCommonObjStoreFlags(cmd, "to", false)

	matcherStrs := cmd.Flag("matcher", "Only blocks whose labels match this matcher will be replicated.").PlaceHolder("key=\"value\"").Strings()

	resolution := cmd.Flag("resolution", "Only blocks with this resolution will be replicated.").Default(strconv.FormatInt(downsample.ResLevel0, 10)).Int64()
	compaction := cmd.Flag("compaction", "Only blocks with this compaction level will be replicated.").Default("1").Int()

	singleRun := cmd.Flag("single-run", "Run replication only one time, then exit.").Default("false").Bool()

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
			matchers,
			compact.ResolutionLevel(*resolution),
			*compaction,
			fromObjStoreConfig,
			toObjStoreConfig,
			*singleRun,
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
	resolution compact.ResolutionLevel,
	compaction int,
	fromObjStoreConfig *extflag.PathOrContent,
	toObjStoreConfig *extflag.PathOrContent,
	singleRun bool,
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

	fromBkt, err := client.NewBucket(
		logger,
		fromConfContentYaml,
		prometheus.WrapRegistererWith(prometheus.Labels{"replicate": "from"}, reg),
		replicateComponent,
	)
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

	toBkt, err := client.NewBucket(
		logger,
		toConfContentYaml,
		prometheus.WrapRegistererWith(prometheus.Labels{"replicate": "to"}, reg),
		replicateComponent,
	)
	if err != nil {
		return err
	}

	replicationRunCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "thanos_replicate_replication_runs_total",
		Help: "The number of replication runs split by success and error.",
	}, []string{"result"})

	replicationRunDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "thanos_replicate_replication_run_duration_seconds",
		Help: "The Duration of replication runs split by success and error.",
	}, []string{"result"})

	reg.MustRegister(replicationRunCounter)
	reg.MustRegister(replicationRunDuration)

	blockFilter := NewBlockFilter(
		logger,
		labelSelector,
		resolution,
		compaction,
	).Filter
	metrics := newReplicationMetrics(reg)
	ctx, cancel := context.WithCancel(context.Background())

	replicateFn := func() error {
		timestamp := time.Now()
		entropy := ulid.Monotonic(rand.New(rand.NewSource(timestamp.UnixNano())), 0)

		ulid, err := ulid.New(ulid.Timestamp(timestamp), entropy)
		if err != nil {
			return errors.Wrap(err, "generate replication run-id")
		}

		logger := log.With(logger, "replication-run-id", ulid.String())
		level.Info(logger).Log("msg", "running replication attempt")

		if err := newReplicationScheme(logger, metrics, blockFilter, fromBkt, toBkt).execute(ctx); err != nil {
			return fmt.Errorf("replication execute: %w", err)
		}

		return nil
	}

	g.Add(func() error {
		defer runutil.CloseWithLogOnErr(logger, fromBkt, "from bucket client")
		defer runutil.CloseWithLogOnErr(logger, toBkt, "to bucket client")

		if singleRun {
			return replicateFn()
		}

		return runutil.Repeat(time.Minute, ctx.Done(), func() error {
			start := time.Now()
			if err := replicateFn(); err != nil {
				level.Error(logger).Log("msg", "running replication failed", "err", err)
				replicationRunCounter.WithLabelValues("error").Inc()
				replicationRunDuration.WithLabelValues("error").Observe(time.Since(start).Seconds())

				// No matter the error we want to repeat indefinitely.
				return nil
			}
			replicationRunCounter.WithLabelValues("success").Inc()
			replicationRunDuration.WithLabelValues("success").Observe(time.Since(start).Seconds())
			level.Info(logger).Log("msg", "ran replication successfully")

			return nil
		})
	}, func(error) {
		cancel()
	})

	level.Info(logger).Log("msg", "starting replication")

	return nil
}
