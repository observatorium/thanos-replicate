package main

import (
	"context"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/oklog/run"
	opentracing "github.com/opentracing/opentracing-go"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/thanos-io/thanos/pkg/component"
	"github.com/thanos-io/thanos/pkg/objstore/client"
	"github.com/thanos-io/thanos/pkg/runutil"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
)

func registerReplicate(m map[string]setupFunc, app *kingpin.Application, name string) {
	cmd := app.Command(name, "Runs replication as a long running daemon.")

	httpMetricsBindAddr := regHTTPAddrFlag(cmd)

	fromObjStoreConfig := regCommonObjStoreFlags(cmd, "from", false)
	toObjStoreConfig := regCommonObjStoreFlags(cmd, "to", false)

	m[name] = func(g *run.Group, logger log.Logger, reg *prometheus.Registry, tracer opentracing.Tracer, _ bool) error {
		return runReplicate(
			g,
			logger,
			reg,
			tracer,
			*httpMetricsBindAddr,
			fromObjStoreConfig,
			toObjStoreConfig,
		)
	}
}

func runReplicate(
	g *run.Group,
	logger log.Logger,
	reg *prometheus.Registry,
	tracer opentracing.Tracer,
	httpMetricsBindAddr string,
	fromObjStoreConfig *pathOrContent,
	toObjStoreConfig *pathOrContent,
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

	fromBkt, err := client.NewBucket(logger, fromConfContentYaml, reg, component.Sidecar.String())
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

	toBkt, err := client.NewBucket(logger, toConfContentYaml, reg, component.Sidecar.String())
	if err != nil {
		return err
	}


	ctx, cancel := context.WithCancel(context.Background())
	g.Add(func() error {
		defer runutil.CloseWithLogOnErr(logger, fromBkt, "from bucket client")
		defer runutil.CloseWithLogOnErr(logger, toBkt, "to bucket client")

		return runutil.Repeat(time.Minute, ctx.Done(), func() error {
			// actually replicate here
			return nil
		})
	}, func(error) {
		cancel()
	})

	level.Info(logger).Log("msg", "starting replication")

	return nil
}
