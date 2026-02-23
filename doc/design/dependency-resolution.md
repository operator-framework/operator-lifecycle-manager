# Dependency Resolution and Upgrades

OLM manages the dependency resolution and upgrade lifecycle of running operators. In many ways, these problems OLM faces are similar to other OS package managers like `apt`/`dkpg` and `yum`/`rpm`.

However, there is one constraint that similar systems don't generally have that OLM does: because operators are always running, OLM attempts to ensure that at no point in time are you left with a set of operators that do not work with each other.

This means that OLM needs to never:

 - install a set of operators that require APIs that can't be provided
 - update an operator in a way that breaks another that depends upon it

The following examples motivate why OLM's dependency resolution and upgrade strategy works as it does, followed by a description of the current algorithm.

## CustomResourceDefinition (CRD) Upgrade

OLM will upgrade CRD right away if it is owned by singular CSV. If CRD is owned by multiple CSVs, then CRD is upgraded when it is
satisfied all of the following backward compatible conditions:

- All existing serving versions in current CRD are present in new CRD
- All existing instances (Custom Resource) that are associated with serving versions of CRD are valid when validated against new CRD's validation schema

### Add a new version to CRD

The recommended procedure to add a new version in CRD:

1. For example, the current CRD has one version `v1alpha1` and you want to add a new version `v1beta1` and mark it as the new storage version:

```
versions:
  - name: v1alpha1
    served: true
    storage: false
  - name: v1beta1
    served: true
    storage: true
```

Note: In `apiextensions.k8s.io/v1beta1`, there was a version field instead of versions. The version field is deprecated and optional, but if it is not empty, it must match the first item in the versions field.

```
version: v1beta1
versions:
  - name: v1beta1
    served: true
    storage: true
  - name: v1alpha1
    served: true
    storage: false
```

2. Ensure the referencing version of CRD in CSV is updated if CSV intends to use the new version in `owned` section:

```
customresourcedefinitions:
  owned:
  - name: cluster.example.com
    version: v1beta1
    kind: cluster
    displayName: Cluster
```

3. Push the updated CRD and CSV to your bundle

### Deprecate/Remove a version of CRD

OLM will not allow a serving version of CRD to be removed right away. Instead, a deprecated version of CRD should have been disabled first by marking `Served` field in CRD to `false` first. Then, the non-serving version can be removed on the subsequent CRD upgrade.

The recommended procedure to deprecate and remove a specific version in CRD:

1. Mark the deprecated version as non-serving to indicate this version is no longer in use and may be removed in subsequent upgrade. For example:

```
versions:
  - name: v1alpha1
    served: false
    storage: true
```

2. Switch storage version to a serving version if soon-to-deprecated version is currently the storage version.
For example:

```
versions:
  - name: v1alpha1
    served: false
    storage: false
  - name: v1beta1
    served: true
    storage: true
```

3. Upgrade CRD with above changes.

4. In subsequent upgrade cycles, the non-serving version can be removed completely from CRD. For example:

```
versions:
  - name: v1beta1
    served: true
    storage: true
```

Note:

1. In order to remove a specific version that is or was storage version from CRD, that version needs to be removed from
`storedVersion` in CRD's status. OLM will attempt to do this for you if it detects a stored version no longer exists in new CRD.

2. Ensure referencing CRD's version in CSV is updated if that version is removed from the CRD.

# Example: Deprecate dependant API

A and B are APIs (e.g. CRDs)

* A's provider depends on B
* B’s provider has a Subscription
* B’s provider updates to provide C but deprecates B

This results in:

* B no longer has a provider
* A no longer works

This is a case we prevent with OLM's upgrade strategy.


# Example: Version deadlock

A and B are APIs

* A's provider requires B
* B's provider requires A
* A's provider updates to (provide A2, require B2) and deprecate A
* B's provider updates to (provide B2, require A2) and deprecate B

If we attempt to update A without simultaneously updating B, or vice-versa, we won't be able to progress to new versions of the operators, even though a new compatible set can be found.

This is another case we prevent with OLM's upgrade strategy.


# Dependency resolution

A Provider is an operator which "Owns" a CRD or APIService.

This algorithm will result in a successful update of a generation (in which as many operators which can be updated have been):

```
Consider the set of operators defined by running operators in a namespace:

  For each subscription in the namespace:
     if the subscription hasn't been checked before, find the latest CSV in the source/package/channel
        provisionally add the operator to the generation
     else
        check for a replacement in the source/package/channel

  // Generation resolution
  For each required API with no provider in gen:
    search through prioritized sources to pick a provider
    provisionally add any new operators found to the generation, this could also add to the required APIs without providers

  // Downgrade
  if there are still required APIs that can't be satisfied by sources:
    downgrade the operator(s) that require the APIs that can't be satisfied

  // Apply
  for each new operator required, install it into the cluster. Any newly resolved operator will be given a subscription to the channel/package/source it was discovered in.
```

The operator expansion loop is bounded by the total number of provided apis across sources (because a generation may not have multiple providers)

The downgrade loop will eventually stop, though it may contract back down to the original generation in the namespace. Downgrading an operator means it was in the previous generation. By definition, either its required apis are satisfied, or will be satisfied by the downgrade of another operator.
