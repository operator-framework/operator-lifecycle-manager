# Adding Priority Classes

## Description

OLM supports users including `PriorityClass` objects in their bundle alongside their operator manifests. `PriorityClass`
is used to establish a priority, or weight, to a collection of pods in order to aid the kube-scheduler when assigning pods
to nodes. For more info, see the docs at https://kubernetes.io/docs/concepts/configuration/pod-priority-preemption/#priorityclass. 

## Caveats

`PriorityClasses` are useful but also potentially far-reaching in nature. Be sure to understand the state of your cluster and
your scheduling requirements before including one in your bundle alongside your operator. Best practice would be to 
include a `PriorityClass` that only affects pods like your operator deployment and the respective operands. 

`PriorityClass` objects are clusterwide in scope, meaning they can affect the scheduling of pods in all namespaces. Operators that specify a PriorityClass can affect other tenants on a multi-tenant cluster.
All pods have a default priority of zero, and only those pods explicitly selected by the `PriorityClass` object will be given a priority when created.
Existing pods running on the cluster are not affected by a new `PriorityClass`, but since clusters are dynamic and pods can be 
rescheduled as nodes cycle in and out, a `PriorityClass` can have an impact on the long term behavior of the cluster. 

Only one `PriorityClass` object in the cluster is allowed to have the `globalDefault` setting set to true. Attempting to install a `PriorityClass` with `globalDefault` set to true when one
with `globalDefault` already exists on-cluster will result in a Forbidden error from the api-server. Setting `globalDefault` on a `PriorityClass` means that all pods in the cluster
without an explicit priority class will use this default `PriorityClass`. 

Pods with higher priorities can preempt pods with lower priorities when they are being scheduled onto nodes: preemption can result in lower-priority pods being evicted to make room for the higher priority pod. 
If the `PriorityClass` of the pod is extremely high (higher than the priority of core components) scheduling the pod can potentially disrupt core components running in the cluster. 

Once a `PriorityClass` is removed, no further pods can be created that reference the deleted `PriorityClass`. 

## Technical Details

`PriorityClass` yaml manifests can be placed in the bundle alongside existing manifests in the `/manifests` directory. The `PriorityClass` manifest will be present
in the bundle image. 

`PriorityClass` objects are clusterwide in scope, and will be applied by OLM directly to the cluster. The `PriorityClass` object will have
a label referencing the operator that it is associated with. 

OLM installs additional objects in the bundle after installing the CRDs and the CSV, to ensure proper owner references between the objects
and the CSV. Therefore, there may be an initial period where additional objects are not available to the operator. 

Prior versions of OLM (pre-0.16.0) do not support `PriorityClass` objects. If a `PriorityClass` is present in a bundle attempting to be installed on-cluster, OLM will throw an invalid installplan error
specifying that the resource is unsupported. 

## Limitations on Priority Classes 

No limitations are placed on the contents of a `PriorityClass` manifest at this time when installing on-cluster, but that may change as OLM develops
an advanced strategy to ensure installed objects do not compromise the cluster. 

However, the following is a suggested guideline to follow when including `PriorityClass` objects in a bundle. 
* `globalDefault` should always be `false` on a `PriorityClass` included in a bundle.
    * Setting `globalDefault` on a `PriorityClass` means that all pods in the cluster without an explicit priority class will use this default `PriorityClass`. 
    This can unintentionally affect other pods running in the cluster. 