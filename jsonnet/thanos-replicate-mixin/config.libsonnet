{
  _config+:: {
    thanosReplicateJobPrefix: 'thanos-replicate',
    thanosReplicateSelector: 'job=~"%s.*"' % self.thanosReplicateJobPrefix,

    grafanaThanos: {
      dashboardNamePrefix: 'Thanos / ',
      dashboardTags: ['thanos-replicate-mixin', 'observatorium'],

      dashboardReplicateTitle: '%(dashboardNamePrefix)sReplicate' % $._config.grafanaThanos,
    },
  },
}
