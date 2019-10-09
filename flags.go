package main

import (
	"fmt"
	"strings"

	"github.com/thanos-io/thanos/pkg/extflag"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
)

func regHTTPAddrFlag(cmd *kingpin.CmdClause) *string {
	return cmd.Flag("http-address", "Listen host:port for HTTP endpoints.").Default("0.0.0.0:10902").String()
}

func regCommonObjStoreFlags(cmd *kingpin.CmdClause, suffix string, required bool, extraDesc ...string) *extflag.PathOrContent {
	help := fmt.Sprintf("YAML file that contains object store%s configuration. See format details: https://thanos.io/storage.md/#configuration ", suffix)
	help = strings.Join(append([]string{help}, extraDesc...), " ")

	return extflag.RegisterPathOrContent(cmd, fmt.Sprintf("objstore%s.config", suffix), help, required)
}

func regCommonTracingFlags(app *kingpin.Application) *extflag.PathOrContent {
	return extflag.RegisterPathOrContent(
		app,
		"tracing.config",
		fmt.Sprintf("YAML file with tracing configuration. See format details: https://thanos.io/tracing.md/#configuration "),
		false,
	)
}
