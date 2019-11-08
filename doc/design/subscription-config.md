# Subscription Config

## Configuring Operators deployed by OLM

It is possible to configure how OLM deploys an operator via the `config` field in the [subscription](../../pkg/api/apis/operators/subscription_types.go) object.

Currently, the OLM supports the following configurations:

### Env

The `Env` field defines a list of environment variables that must exist in all containers in the `pod` created by OLM.

> Note: Values defined here will overwrite existing environment variables of the same name.

### EnvFrom

The `EnvFrom` field defines a list of sources to populate environment variables in the container. 
The keys defined within a source must be a C_IDENTIFIER. 
All invalid keys will be reported as an event when the container is starting.
When a key exists in multiple sources, the value associated with the last source will take precedence.

> Note: Values defined by an Env with a duplicate key will take precedence.

### Volumes

The `Volumes` field defines a list of [volumes](https://kubernetes.io/docs/concepts/storage/volumes/) that must exist on the `pod` created by OLM.

> Note: Volumes defined here will overwrite existing Volumes of the same name.

### VolumeMounts

The `VolumeMounts` field defines a list of [volumeMounts](https://kubernetes.io/docs/concepts/storage/volumes/) that must exist in all containers in the `pod` created by OLM. If a `volumeMount` references a `volume` that does not exist, OLM will fail to deploy the operator.

> Note: VolumeMounts defined here will overwrite existing VolumeMounts of the same name.
