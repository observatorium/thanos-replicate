{
  local thanos = self,
  replicator+:: {
    jobPrefix: error 'must provide job prefix for Thanos Replicate dashboard',
    selector: error 'must provide selector for Thanos Replicate dashboard',
    title: error 'must provide title for Thanos Replicate dashboard',
  },
  prometheusRules+:: {
    groups+: [
      {
        name: 'thanos-replicate.rules',
        rules: [
        ],
      },
    ],
  },
}
