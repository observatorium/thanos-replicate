package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/oklog/run"
	"github.com/opentracing/opentracing-go"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/model"
	"github.com/prometheus/common/version"
	"github.com/prometheus/prometheus/tsdb/labels"
	"github.com/thanos-io/thanos/pkg/runutil"
	"github.com/thanos-io/thanos/pkg/tracing/client"
	"go.uber.org/automaxprocs/maxprocs"
	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	logFormatLogfmt = "logfmt"
	logFormatJSON   = "json"
)

type setupFunc func(*run.Group, log.Logger, *prometheus.Registry, opentracing.Tracer, bool) error

func logger(logLevel, logFormat, debugName string) log.Logger {
	var (
		logger log.Logger
		lvl    level.Option
	)

	switch logLevel {
	case "error":
		lvl = level.AllowError()
	case "warn":
		lvl = level.AllowWarn()
	case "info":
		lvl = level.AllowInfo()
	case "debug":
		lvl = level.AllowDebug()
	default:
		panic("unexpected log level")
	}

	logger = log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	if logFormat == logFormatJSON {
		logger = log.NewJSONLogger(log.NewSyncWriter(os.Stderr))
	}

	logger = level.NewFilter(logger, lvl)

	if debugName != "" {
		logger = log.With(logger, "name", debugName)
	}

	return log.With(logger, "ts", log.DefaultTimestampUTC, "caller", log.DefaultCaller)
}

func main() {
	if os.Getenv("DEBUG") != "" {
		runtime.SetMutexProfileFraction(10)
		runtime.SetBlockProfileRate(10)
	}

	app := kingpin.New(filepath.Base(os.Args[0]), "A replication scheme to replicate raw time-series blocks from one Thanos instance to another.")

	app.Version(version.Print("thanos-replicate"))
	app.HelpFlag.Short('h')

	debugName := app.Flag("debug.name", "Name to add as prefix to log lines.").Hidden().String()

	logLevel := app.Flag("log.level", "Log filtering level.").
		Default("info").Enum("error", "warn", "info", "debug")
	logFormat := app.Flag("log.format", "Log format to use.").
		Default(logFormatLogfmt).Enum(logFormatLogfmt, logFormatJSON)

	tracingConfig := regCommonTracingFlags(app)

	cmds := map[string]setupFunc{}
	registerReplicate(cmds, app, "run")

	cmd, err := app.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, errors.Wrapf(err, "Error parsing commandline arguments"))
		app.Usage(os.Args[1:])
		os.Exit(2)
	}

	logger := logger(*logLevel, *logFormat, *debugName)
	loggerAdapter := func(template string, args ...interface{}) {
		level.Debug(logger).Log("msg", fmt.Sprintf(template, args))
	}

	// Running in container with limits but with empty/wrong value of GOMAXPROCS env var could lead to throttling by cpu
	// maxprocs will automate adjustment by using cgroups info about cpu limit if it set as value for runtime.GOMAXPROCS
	undo, err := maxprocs.Set(maxprocs.Logger(loggerAdapter))
	if err != nil {
		fmt.Fprintln(os.Stderr, errors.Wrapf(err, "failed to set GOMAXPROCS: %v", err))
	}

	defer undo()

	metrics := prometheus.NewRegistry()
	metrics.MustRegister(
		version.NewCollector("thanos_replicate"),
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)

	prometheus.DefaultRegisterer = metrics

	var (
		g      run.Group
		tracer opentracing.Tracer
	)

	// Setup optional tracing.
	{
		ctx := context.Background()

		var closer io.Closer
		var confContentYaml []byte
		confContentYaml, err = tracingConfig.Content()
		if err != nil {
			level.Error(logger).Log("msg", "getting tracing config failed", "err", err)
			os.Exit(1)
		}

		if len(confContentYaml) == 0 {
			level.Info(logger).Log("msg", "Tracing will be disabled")
			tracer = client.NoopTracer()
		} else {
			tracer, closer, err = client.NewTracer(ctx, logger, metrics, confContentYaml)
			if err != nil {
				fmt.Fprintln(os.Stderr, errors.Wrapf(err, "tracing failed"))
				os.Exit(1)
			}
		}

		// This is bad, but Prometheus does not support any other tracer injections than just global one.
		opentracing.SetGlobalTracer(tracer)

		ctx, cancel := context.WithCancel(ctx)
		g.Add(func() error {
			<-ctx.Done()
			return ctx.Err()
		}, func(error) {
			if closer != nil {
				if err := closer.Close(); err != nil {
					level.Warn(logger).Log("msg", "closing tracer failed", "err", err)
				}
			}
			cancel()
		})
	}

	if err := cmds[cmd](&g, logger, metrics, tracer, *logLevel == "debug"); err != nil {
		level.Error(logger).Log("err", errors.Wrapf(err, "%s command failed", cmd))
		os.Exit(1)
	}

	// Listen for termination signals.
	{
		cancel := make(chan struct{})
		g.Add(func() error {
			return interrupt(logger, cancel)
		}, func(error) {
			close(cancel)
		})
	}

	if err := g.Run(); err != nil {
		level.Error(logger).Log("msg", "running command failed", "err", err)
		os.Exit(1)
	}

	level.Info(logger).Log("msg", "exiting")
	os.Exit(0)
}

func interrupt(logger log.Logger, cancel <-chan struct{}) error {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	select {
	case s := <-c:
		level.Info(logger).Log("msg", "caught signal. Exiting.", "signal", s)
		return nil
	case <-cancel:
		return errors.New("canceled")
	}
}

func registerProfile(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/debug/pprof/block", pprof.Handler("block"))
	mux.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	mux.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	mux.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
}

func registerMetrics(mux *http.ServeMux, g prometheus.Gatherer) {
	mux.Handle("/metrics", promhttp.HandlerFor(g, promhttp.HandlerOpts{}))
}

// metricHTTPListenGroup is a run.Group that servers HTTP endpoint with only Prometheus metrics.
func metricHTTPListenGroup(g *run.Group, logger log.Logger, reg prometheus.Gatherer, httpBindAddr string) error {
	mux := http.NewServeMux()
	registerMetrics(mux, reg)
	registerProfile(mux)

	l, err := net.Listen("tcp", httpBindAddr)
	if err != nil {
		return errors.Wrap(err, "listen metrics address")
	}

	g.Add(func() error {
		level.Info(logger).Log("msg", "Listening for metrics", "address", httpBindAddr)
		return errors.Wrap(http.Serve(l, mux), "serve metrics")
	}, func(error) {
		runutil.CloseWithLogOnErr(logger, l, "metric listener")
	})

	return nil
}

func parseFlagMatchers(s []string) ([]labels.Matcher, error) {
	matchers := make([]labels.Matcher, 0, len(s))

	for _, l := range s {
		parts := strings.SplitN(l, "=", 2)
		if len(parts) != 2 {
			return nil, errors.Errorf("unrecognized label %q", l)
		}

		labelName := parts[0]
		if !model.LabelName.IsValid(model.LabelName(labelName)) {
			return nil, errors.Errorf("unsupported format for label %s", l)
		}

		labelValue, err := strconv.Unquote(parts[1])
		if err != nil {
			return nil, errors.Wrap(err, "unquote label value")
		}

		matchers = append(matchers, labels.NewEqualMatcher(labelName, labelValue))
	}

	return matchers, nil
}
