{
  local thanos = self,
  replicator+:: {
    jobPrefix: error 'must provide job prefix for Thanos Replicate dashboard',
    selector: error 'must provide selector for Thanos Replicate dashboard',
    title: error 'must provide title for Thanos Replicate dashboard',
  },
  prometheusAlerts+:: {
    groups+: [
      {
        name: 'thanos-replicate.rules',
        rules: [
          {
            alert: 'ThanosReplicateIsDown',
            expr: |||
              absent(up{%(selector)s})
            ||| % thanos.replicator,
            'for': '5m',
            labels: {
              severity: 'critical',
            },
            annotations: {
              message: 'Thanos Replicate has disappeared from Prometheus target discovery.',
            },
          },
          {
            alert: 'ThanosReplicateErrorRate',
            annotations: {
              message: 'Thanos Replicate failing to run, {{ $value | humanize }}% of attempts failed.',
            },
            expr: |||
              (
                sum(rate(thanos_replicate_replication_runs_total{result="error", %(selector)s}[5m]))
              / on (namespace) group_left
                sum(rate(thanos_replicate_replication_runs_total{%(selector)s}[5m]))
              ) * 100 >= 10
            ||| % thanos.replicator,
            'for': '5m',
            labels: {
              severity: 'critical',
            },
          },
          {
            alert: 'ThanosReplicateRunLatency',
            annotations: {
              message: 'Thanos Replicate {{$labels.job}} has a 99th percentile latency of {{ $value }} seconds for the replicate operations.',
            },
            expr: |||
              (
                histogram_quantile(0.9, sum by (job, le) (thanos_replicate_replication_run_duration_seconds_bucket{%(selector)s})) > 120
              and
                sum by (job) (rate(thanos_replicate_replication_run_duration_seconds_bucket{%(selector)s}[5m])) > 0
              )
            ||| % thanos.replicator,
            'for': '5m',
            labels: {
              severity: 'critical',
            },
          },
        ],
      },
    ],
  },
}
