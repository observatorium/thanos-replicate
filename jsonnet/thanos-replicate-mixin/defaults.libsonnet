{
  replicator+:: {
    jobPrefix: 'thanos-replicate',
    selector: 'job=~"%s.*"' % self.jobPrefix,
    title: '%(prefix)sReplicator' % $.dashboard.prefix,
  },
  dashboard+:: {
    prefix: 'Thanos / ',
    tags: ['thanos-replicate-mixin', 'observatorium'],
    namespaceQuery: 'kube_pod_info',
  },
}
