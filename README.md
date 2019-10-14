# thanos-replicate

This project allows replicating blocks of time-series data produced by [Thanos](https://thanos.io/) from one object storage bucket to another.

## Usage

To setup replication from one bucket to another use the [typical bucket specification](https://thanos.io/storage.md/#configuration) of Thanos.

[embedmd]:# (tmp/help.txt)
```txt
usage: thanos-replicate run [<flags>]

Runs replication as a long running daemon.

Flags:
  -h, --help                     Show context-sensitive help (also try
                                 --help-long and --help-man).
      --version                  Show application version.
      --log.level=info           Log filtering level.
      --log.format=logfmt        Log format to use.
      --tracing.config-file=<file-path>  
                                 Path to YAML file with tracing configuration.
                                 See format details:
                                 https://thanos.io/tracing.md/#configuration
      --tracing.config=<content>  
                                 Alternative to 'tracing.config-file' flag
                                 (lower priority). Content of YAML file with
                                 tracing configuration. See format details:
                                 https://thanos.io/tracing.md/#configuration
      --http-address="0.0.0.0:10902"  
                                 Listen host:port for HTTP endpoints.
      --objstorefrom.config-file=<file-path>  
                                 Path to YAML file that contains object
                                 storefrom configuration. See format details:
                                 https://thanos.io/storage.md/#configuration
      --objstorefrom.config=<content>  
                                 Alternative to 'objstorefrom.config-file' flag
                                 (lower priority). Content of YAML file that
                                 contains object storefrom configuration. See
                                 format details:
                                 https://thanos.io/storage.md/#configuration
      --objstoreto.config-file=<file-path>  
                                 Path to YAML file that contains object storeto
                                 configuration. See format details:
                                 https://thanos.io/storage.md/#configuration
      --objstoreto.config=<content>  
                                 Alternative to 'objstoreto.config-file' flag
                                 (lower priority). Content of YAML file that
                                 contains object storeto configuration. See
                                 format details:
                                 https://thanos.io/storage.md/#configuration
      --matcher=key="value" ...  Only blocks whose labels match this matcher
                                 will be replicated.

```
