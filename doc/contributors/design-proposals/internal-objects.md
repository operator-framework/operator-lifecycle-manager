# Hiding Internal or Data-only Objects

Status: Pending

Version: Alpha

Implementation Owner: unassigned 

# Motivation

Operators often use CRDs internally. These objects aren't meant for end-users to manipulate and are confusing to end users of the Operator.

## Proposal

Introduce a convention for marking these CRDs as "internal" or "data only", so they can omitted from user interfaces and CLI tools.

A good example of this is the [Apache CouchDB Operator](https://operatorhub.io/operator/couchdb-operator) which has literally marked the CRD name with the "internal" moniker, eg `(Internal) CouchDB Formation`.

### Implementation

#### New CSV annotation

The behavior is straightforward, when a CRD is managed as part of the Operator installation, it can be marked as managed by the Operator, but also that it is an internal resource, not to be used by an end-user. This is an annotation on the CSV of `spec.owned.customresourcedefinitions.name` names, which is available for downstream tools to read and hide the CRD where applicable. This should be backwards compatible as a no-op, so it can be considered progressive enhancement.

```
apiVersion: operators.coreos.com/v1alpha1
kind: ClusterServiceVersion
metadata:
  name: couchdb-operator-v1.2.3
  annotations:
    apps.operatorframework.io/internal-objects:
      - '(Internal) CouchDB Formation Lock'
      - '(Internal) CouchDB Recipe'
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

If this Replication CRD is not meant for manipulation by end-users, it can be hidden by including its name within the `apps.operatorframework.io/internal-objects` annotation's array of values.

If there exists a CRD that is only meant for tracking data, it can also be included within the  `app.operatorframework.io/data-object` array.

Before marking one of your CRDs as internal, make sure that any debugging information or configuration that might be required to manage the application is reflected on the CR's status or spec block.
