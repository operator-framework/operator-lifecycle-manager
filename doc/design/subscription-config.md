# Subscription Config

## Configuring Operators deployed by OLM

It is possible to configure how OLM deploys an Operator via the `config` field in the [Subscription](https://github.com/operator-framework/olm-book/blob/master/docs/subscriptions.md) object.

Currently, OLM supports the following configurations:

### Env

The `env` field defines a list of [Environment Variables](https://kubernetes.io/docs/tasks/inject-data-application/define-environment-variable-container/#define-an-environment-variable-for-a-container) that must exist in all containers in the Pod created by OLM.

> Note: Values defined here will overwrite existing environment variables of the same name.

#### Example

Increase log verbosity on an Operator's container that utilizes the `ARGS` variable:

```yaml
kind: Subscription
metadata:
  name: prometheus
spec:
  package: prometheus
  channel: alpha
  config:
    env:
    - name: ARGS
      value: "-v=10"
```

### EnvFrom

The `envFrom` field defines a [list of sources to populate Environment Variables](https://kubernetes.io/docs/tasks/configure-pod-container/configure-pod-configmap/#configure-all-key-value-pairs-in-a-configmap-as-container-environment-variables) in the container. The keys defined within a source must be a C_IDENTIFIER. All invalid keys will be reported as an event when the container is starting. When a key exists in multiple sources, the value associated with the last source will take precedence.

> Note: Values defined by an Env with a duplicate key will take precedence.

#### Example

Inject a license key residing in a Secret to unlock Operator features:

```yaml
kind: Subscription
metadata:
  name: my-operator
spec:
  package: app-operator
  channel: stable
  config:
    envFrom:
    - secretRef:
        name: license-secret
```

### Volumes

The `volumes` field defines a list of [Volumes](https://kubernetes.io/docs/concepts/storage/volumes/) that must exist on the Pod created by OLM.

> Note: Volumes defined here will overwrite existing Volumes of the same name.

### VolumeMounts

The `volumeMounts` field defines a list of [VolumeMounts](https://kubernetes.io/docs/concepts/storage/volumes/) that must exist in all containers in the Pod created by OLM. If a `volumeMount` references a `volume` that does not exist, OLM will fail to deploy the operator.

> Note: VolumeMounts defined here will overwrite existing VolumeMounts of the same name.

#### Example

Mount a ConfigMap as a Volume that contains configuration information that can change default Operator behavior. Modifications to the content of the ConfigMap should appear within the container's VolumeMount.

```yaml
kind: Subscription
metadata:
  name: my-operator
spec:
  package: etcd
  channel: alpha
  config:
    volumes:
    - name: config-volume
      configMap:
        name: etcd-operator-config
    volumeMounts:
    - mountPath: /config
      name: config-volume
```

### Tolerations

The `tolerations` field defines a list of [Tolerations](https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/) for the Pod created by OLM.

> Note: Tolerations defined here will be appended to existing Tolerations, if not already present.

#### Example

Inject toleration to tolerate all taints.

```yaml
kind: Subscription
metadata:
  name: my-operator
spec:
  package: etcd
  channel: alpha
  config:
    tolerations:
    - operator: "Exists"
```

### Resources

The `resources` field defines [Resource Constraints](https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container) for all the containers in the Pod created by OLM.

> Note: Resource Constraints defined here will overwrite existing resource constraints.

#### Example

Inject a request of 0.25 cpu and 64 MiB of memory, and a limit of 0.5 cpu and 128MiB of memory in each container.

```yaml
kind: Subscription
metadata:
  name: my-operator
spec:
  package: etcd
  channel: alpha
  config:
    resources:
      requests:
        memory: "64Mi"
        cpu: "250m"
      limits:
        memory: "128Mi"
        cpu: "500m"
```

### NodeSelector

The `nodeSelector` field defines a [NodeSelector](https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/) for the Pod created by OLM.

#### Example

Inject `nodeSelector` key-values pairs.

```yaml
kind: Subscription
metadata:
  name: my-operator
spec:
  package: etcd
  channel: alpha
  config:
    nodeSelector:
      foo: bar
```
