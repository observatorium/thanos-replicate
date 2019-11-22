{
  prometheusAlerts+:: {
    groups+: [
      {
        name: 'thanos-replicate.rules',
        rules: [
          {
            alert: 'ThanosReplicateIsDown',
            expr: |||
              absent(up{%(thanosReplicateSelector)s})
            ||| % $._config,
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
                sum(rate(thanos_replicate_replication_runs_total{result="error", %(thanosReplicateSelector)s}))
              / on (namespace) group_left
                sum(rate(thanos_replicate_replication_runs_total{%(thanosReplicateSelector)s}))
              ) * 100 >= 10
            ||| % $._config,
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
