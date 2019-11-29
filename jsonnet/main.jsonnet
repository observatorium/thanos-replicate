local app = (import 'lib/thanos-replicate.libsonnet') {
  thanos+:: {
    namespace:: 'observatorium',

    fromObjectStorageConfig+:: {
      name: 'name',
      key: 'key',
    },

    objectStorageConfig+:: {
      name: 'name',
      key: 'key',
    },
  },
};

{ [name]: app.thanos.replicate[name] for name in std.objectFields(app.thanos.replicate) }
