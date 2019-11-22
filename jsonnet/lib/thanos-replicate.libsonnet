local k = import 'ksonnet/ksonnet.beta.4/k.libsonnet';

{
  thanos+:: {
    namespace:: error 'must set namespace',

    fromObjectStorageConfig+:: {
      name: error 'must set an object storage secret name',
      key: error 'must set an object storage secret key',
    },

    objectStorageConfig+:: {
      name: error 'must set an object storage secret name',
      key: error 'must set an object storage secret key',
    },

    replicate+: {
      local tr = self,
      s3Secret:: 'thanos-s3-reader',

      name:: 'thanos-replicate',
      namespace:: $.thanos.namespace,
      image:: 'quay.io/observatorium/thanos-replicate:master-2019-10-25-1f8a062',
      labels+:: {
        'app.kubernetes.io/name': tr.name,
      },

      service+:
        local service = k.core.v1.service;
        local ports = service.mixin.spec.portsType;

        service.new(tr.name, tr.labels, [
          ports.newNamed('http', 10902, 10902),
        ]),

      statefulset+:
        local sts = k.apps.v1.statefulSet;
        local container = sts.mixin.spec.template.spec.containersType;
        local containerEnv = container.envType;
        local volume = sts.mixin.spec.template.spec.volumesType;

        local c =
          container.new(tr.name, tr.image) +
          container.withArgs([
            'run',
            '--objstorefrom.config=$(REPLICATE_OBJSTORE_CONFIG)',
            '--objstoreto.config=$(OBJSTORE_CONFIG)',
            '--log.level=debug',
          ]) +
          container.withEnv([
            containerEnv.fromSecretRef(
              'OBJSTORE_CONFIG',
              $.thanos.objectStorageConfig.name,
              $.thanos.objectStorageConfig.key,
            ),
            containerEnv.fromSecretRef(
              'REPLICATE_OBJSTORE_CONFIG',
              $.thanos.fromObjectStorageConfig.name,
              $.thanos.fromObjectStorageConfig.key,
            ),
            containerEnv.fromSecretRef('AWS_ACCESS_KEY_ID', tr.s3Secret, 'aws_access_key_id'),
            containerEnv.fromSecretRef('AWS_SECRET_ACCESS_KEY', tr.s3Secret, 'aws_secret_access_key'),
          ]) +
          container.withPorts([
            { name: 'http', containerPort: $.thanos.replicate.service.spec.ports[0].port },
          ]) +
          container.mixin.resources.withLimits({ cpu: '500m', memory: '5Gi' }) +
          container.mixin.resources.withRequests({ cpu: '300m', memory: '1Gi' });

        sts.new(tr.name, 1, c, null, tr.labels) +
        sts.mixin.metadata.withNamespace(tr.namespace) +
        sts.mixin.spec.selector.withMatchLabels(tr.labels) +
        sts.mixin.spec.withServiceName(tr.name) +
        {
          spec+: {
            volumeClaimTemplates:: null,
          },
        },
    },
  },
}
