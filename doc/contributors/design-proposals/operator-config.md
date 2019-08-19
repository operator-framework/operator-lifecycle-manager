# Operator Configuration

Status: Pending

Version: Alpha

Implementation Owner: tkashem

Prereqs: https://github.com/operator-framework/operator-lifecycle-manager/pull/931

## Motivation

Cluster administrators may need to configure operators beyond the defaults that come via an operator bundle from OLM, such as:

- Pod placement (node selectors)
- Resource requirements and limits
- Tolerations 
- Environment Variables
  - Specifically, Proxy configuration

## Proposal

This configuration could live in a new object, but we are beginning work to consolidate our APIs into a smaller surface. As part of that goal, we will hang new features off of the `Subscription` object. 

In the future, as `Subscription` takes more of a front seat in the apis, it will likely get an alternate name (e.g. `Operator`).

### Subscription Spec Changes

A new section `config` is added to the SubscriptionSpec. (bikeshed note: `podConfig` may be more specific/descriptive)

```yaml
kind: Subscription
metadata:
  name: my-operator
spec:
  pacakge: etcd
  channel: alpha
  
  # new
  config:
  - selector:                     
      matchLabels:
        app: etcd-operator
    resources:
      requests:
        memory: "64Mi"
        cpu: "250m"
      limits:
        memory: "128Mi"
        cpu: "500m"
    nodeSelector:
      disktype: ssd
    tolerations:
    - key: "key"
      operator: "Equal"
      value: "value"
      effect: "NoSchedule"
    # provide application config via a configmap volume
    volumes:
    - name: config-volume
      configMap:
        name: etcd-operator-config
    volumeMounts:
    - mountPath: /config
      name: config-volume
    # provide application config via env variables
    env:
    - name: SPECIAL_LEVEL_KEY
      valueFrom:
        configMapKeyRef:
          name: special-config
          key: special.how
    envFrom:
    - configMapRef:
        name: etcd-env-config
```

### Subscription Status Changes

The subscription status should reflect whether or not the configuration was successfully applied to the operator pods.

New status conditions (abnormal-true):

```yaml
  conditions:
  - message: "SPECIAL_LEVEL_KEY" couldn't be applied...
    reason: EnvFailure
    status: True
    type: ConfigFailure
  - message: No operator pods found matching selector.
    reason: NoMatchingPods
    status: True
    type: PodConfigSelectorFailure
```

Reasons for `ConfigFailure`

 - `EnvFailure`
 - `EnvFromFailure`
 - `VolumeFailure`
 - `VolumeMountFailure`
 - `TolerationFailure`
 - `NodeSelectorFailure`
 - `ResourceRequestFailure`
 - `ResourceLimitFailure`

### Implementation

#### Subscription Spec and Status

Spec and Status need to be updated to include the fields described above, and the openapi validation should be updated as well.

#### Control Loops

#### Install Strategy

Most of the change will take place in the install strategy; which knows how to take the deployment spec defined in a CSV and check if the cluster is up-to-date, and apply changes if needed.

- The install strategy will now need to accept the additional configuration from the subscription.
	- `CheckInstalled` will need to combine the additional config with the deployment spec from the ClusterServiceVersion.
		- Subscription config will overwrite the settings on the CSV.
	- `Install` will also need to combien the additional config with the deployment spec.

Appropriate errors should be returned so that we can construct the status that needed for the subscription config status conditions.

This requires that https://github.com/operator-framework/operator-lifecycle-manager/pull/931 have merged, so that changes to a deployment are persisted to the cluster.
	
#### Subscription sync

- Subscription sync will now need to use the install strategy for the installed CSV to determine if the config settings have been properly applied.
	- If not, appropriate status should be written signalling what is missing, and the associated CSV should be requeued. 
	- Requeing the CSV will attempt to reconcile the config with the deployment on the cluster.

#### Openshift-specific implementation

On start, OLM (catalog operator) needs to:

- Check if the [openshift proxy config api](https://github.com/openshift/api/blob/master/config/v1/types_proxy.go) is available
- If so, set up watches / informers for the GVK to keep our view of the global proxy config up to date.

When reconciling an operator's `Deployment`:

 - If the global proxy object is not set / api doesn't exist, do nothing different.
 - If the global proxy object is set and none of `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` are set on the `Subscription`
 	- Then set those env vars on the deployment and ensure the deployment on the cluster matches.
- If the global proxy object is set and at least one of `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` are set on the `Subscription`
	- Then do nothing different. Global proxy config has been overridden by a user.


### User Documentation

#### Operator Pod Configuration

For the most part, operators are packaged so that they require no configuration. But there are cases where you may wish to configure certain aspects of an operator's runtime environment and have that configuration persist between operator updates.

Examples of configuration that can be set for operator pods:

- Node Selectors and Tolerations to direct pods to particular nodes
- Pod resource requirements and limits
- Enabling debug logs for an operator via an operator-specific config flag
- Setting or overriding proxy configuration

This configuration is set on the `Subscription` for the operator in an optional `config` block. `config` is an list of configurations that should be applied to operator pods, with each entry applying to the pods in the operator for the pods that match the selector. (Operators may consist of many pods, and configuration may only apply to a subset of them)

```yaml
  # ... 
  config:
  - selector:                     
      matchLabels:
        app: etcd-operator
    resources:
      requests:
        memory: "64Mi"
        cpu: "250m"
      limits:
        memory: "128Mi"
        cpu: "500m"
    nodeSelector:
      disktype: ssd
    tolerations:
    - key: "key"
      operator: "Equal"
      value: "value"
      effect: "NoSchedule"
    # provide application config via a configmap volume
    volumes:
    - name: config-volume
      configMap:
        name: etcd-operator-config
    volumeMounts:
    - mountPath: /config
      name: config-volume
    # provide application config via env variables
    env:
    - name: SPECIAL_LEVEL_KEY
      valueFrom:
        configMapKeyRef:
          name: special-config
          key: special.how
    envFrom:
    - configMapRef:
        name: etcd-env-config
```

#### Caveats

**Template labels:** Operator configuration is applied via label selectors, and the labels are matched based on the labels configured on the operator at install time (not that exist at runtime on the cluster).

For example, if a ClusterServiceVersion is defined:

```yaml
kind: ClusterServiceVersion
spec:
  install:
    spec:
      deployments:
      - name: prometheus-operator
        spec:
          template:
            metadata:
              labels:
                k8s-app: prometheus-operator
```

An operator pod may be created with lables:

```yaml
metadata:
  labels:
    k8s-app: prometheus-operator
    olm.cahash: 123
```
 
 Then this `Subscription` config with the selector **will match**:
 
 ```yaml
   config:
   - selector:                     
      matchLabels:
        k8s-app: prometheus-operator
 ```

 But this `Subscription` config with the selector **will not match**:
 
 ```yaml
   config:
   - selector:                     
      matchLabels:
        olm.cahash: 123
 ```
 
Because matching is determined from the pod template and not from the real pod on the cluster. Similarly, the configuration will only apply to pods defined by ClusterServiceVersion that has been installed from the Subscription, and will not apply to any other pods, even if the the pod has a matching label.
 
**Upgrades:** Operator configuration is persisted between updates to the operator, but only if the updated operator's pods continue to match the defined selector. Operator authors should not remove previously-defined template labels unless they wish to prevent previously-defined config from applying.

#### Scenario: Enable debug logs for an operator

In this scenario, we have an operator that has been written to have a command flag set to enable debug logs: `-v=4`.

The relevent section of the `ClusterServiceVersion` (note the new `$(ARGS)`)

```yaml
kind: ClusterServiceVersion
spec:
  install:
    spec:
      deployments:
      - name: prometheus-operator
        spec:
          template:
            metadata:
              labels:
                k8s-app: prometheus-operator
            spec:
              containers:
              - name: prometheus-operator
                args:
                - -namespaces=$(NAMESPACES)
                - $(ARGS)
                env:
                - name: NAMESPACES
                  valueFrom:
                    fieldRef:
                      fieldPath: metadata.annotations['olm.targetNamespaces']
```
This can then be configured, optionally, from the subscription:

```yaml
kind: Subscription
metadata:
  name: prometheus
spec:
  pacakge: prometheus
  channel: alpha
  config:
  - selector:                     
      matchLabels:
        k8s-app: prometheus-operator
    env:
    - name: ARGS
      value: "-v=4"
```

The `Deployment` object will then be updated by OLM:

```
kind: Deployment
spec:
  template:
    metadata:
      labels:
        k8s-app: prometheus-operator
    spec:
      containers:
      - name: prometheus-operator
        args:
        - -namespaces=$(NAMESPACES)
        - $(ARGS)
        env:
        - name: NAMESPACES
          valueFrom:
            fieldRef:
              fieldPath: metadata.annotations['olm.targetNamespaces']
        - name: ARGS
          value: "-v=4"
```

When the operator updates to a newer version, it will still be configured with `-v=4	` (though it's up to the operator author whether that continues to have the desired effect).

#### Openshift Notes

When running in Openshift, OLM will fill in the config for env vars:

- `HTTP_PROXY`
- `HTTPS_PROXY`
- `NO_PROXY`

if there is a global `ProxyConfig` object defined in the cluster. These are treated as a unit - if one of them is already defined on a `Subscription`, the others will not be changed if the global `ProxyConfig` is changed.
