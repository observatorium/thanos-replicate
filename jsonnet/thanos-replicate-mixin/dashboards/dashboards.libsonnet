local g = import 'thanos-mixin/lib/thanos-grafana-builder/builder.libsonnet';

{
  grafanaDashboards+:: {
    'replicate.json':
      g.dashboard($._config.grafanaThanos.dashboardReplicateTitle)
      .addRow(
        g.row('Replicate Runs')
        .addPanel(
          g.panel('Rate') +
          g.qpsErrTotalPanel(
            'thanos_replicate_replication_runs_total{result="error", namespace="$namespace",%(thanosReplicateSelector)s}' % $._config,
            'thanos_replicate_replication_runs_total{namespace="$namespace",%(thanosReplicateSelector)s}' % $._config,
          )
        )
        .addPanel(
          g.panel('Errors', 'Shows rate of errors.') +
          g.queryPanel(
            'sum(rate(thanos_replicate_replication_runs_total{result="error", namespace="$namespace",%(thanosReplicateSelector)s}[$interval])) by (result)' % $._config,
            '{{result}}'
          ) +
          { yaxes: g.yaxes('percentunit') } +
          g.stack
        )
        .addPanel(
          g.panel('Duration', 'Shows how long has it taken to run a replication cycle.') +
          g.latencyPanel('thanos_replicate_replication_run_duration_seconds', 'result="success", namespace="$namespace",%(thanosReplicateSelector)s}' % $._config)
        )
      )
      .addRow(
        g.row('Replication')
        .addPanel(
          g.panel('Metrics') +
          g.queryPanel(
            [
              'sum(rate(thanos_replicate_origin_iterations_total{namespace="$namespace",%(thanosReplicateSelector)s}[$interval]))' % $._config,
              'sum(rate(thanos_replicate_origin_meta_loads_total{namespace="$namespace",%(thanosReplicateSelector)s}[$interval]))' % $._config,
              'sum(rate(thanos_replicate_origin_partial_meta_reads_total{namespace="$namespace",%(thanosReplicateSelector)s}[$interval]))' % $._config,
              'sum(rate(thanos_replicate_blocks_already_replicated_total{namespace="$namespace",%(thanosReplicateSelector)s}[$interval]))' % $._config,
              'sum(rate(thanos_replicate_blocks_replicated_total{namespace="$namespace",%(thanosReplicateSelector)s}[$interval]))' % $._config,
              'sum(rate(thanos_replicate_objects_replicated_total{namespace="$namespace",%(thanosReplicateSelector)s}[$interval]))' % $._config,
            ],
            ['', '', '', '', '', '']
          )
        )
      )
      +
      g.template('namespace', 'kube_pod_info') +
      g.template('job', 'up', 'namespace="$namespace",%(thanosReplicateSelector)s' % $._config, true, '%(thanosReplicateJobPrefix)s.*' % $._config),
  },
} +
(import 'defaults.libsonnet')
