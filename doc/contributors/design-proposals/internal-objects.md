# Related Images

Status: Pending

Version: Alpha

Implementation Owner: unassigned 

# Motivation

Operators often use CRDs internally. These objects aren't meant for end-users to manipulate and are confusing to end users of the Operator.

## Proposal

Introduce a convention for marking these CRDs as "internal" or "data only", so they can omitted from user interfaces and CLI tools.

A good example of this is the [Apache CouchDB Operator](https://operatorhub.io/operator/couchdb-operator) which has literally marked the CRD name with the "internal" moniker, eg `(Internal) CouchDB Formation`.

### Implementation

#### New CRD annotation

The behavior is straightforward, when a CRD is managed as part of the Operator installation, it can be marked with an annotation which is available for downstream tools to read and hide the CRD where applicable. This should be backwards compatible as a no-op, so it can be considered progressive enhancement.

```
kind: CustomResourceDefinition
apiVersion: apiextensions.k8s.io/v1beta1
metadata:
  name: hivetables.metering.openshift.io
  annotations:
    apps.kubernetes.io/internal-object:true
    apps.kubernetes.io/data-object:true
spec:
  ...
status
  ...
```

#### Implementation Stages

- [ ] Verify annotations are populated down after installation
- [ ] Document this annotation convention
- [ ] Verify that any Operator pipelines allow use of the annotations

### User Documentation

#### Hiding Internal Concepts from End-Users

It is a common practice for an Operator to utilize CRDs "under the hood" to internally accomplish a task. For example, a database Operator might have a Replication CRD that is created whenever an end-user creates a Database object with `replication: true`. 

If this Replication CRD is not meant for manipulation by end-users, it can be hidden by submitting it's definition with the `apps.kubernetes.io/internal-object` annotation set to true.

If there exists a CRD that is only meant for tracking data, it can also be annotated with `apps.kubernetes.io/data-object` set to true. 

Before marking one of your CRDs as internal, make sure that any debugging information or configuration that might be required to manage the application is reflected on the CR's status or spec block.
