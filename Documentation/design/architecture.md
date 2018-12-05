# Architecture

OLM is composed of two Operators: the OLM Operator and the Catalog Operator.

Each of these Operators are responsible for managing the CRDs that are the basis for the OLM framework:

| Resource                 | Short Name | Owner   | Description                                                                                |
|--------------------------|------------|---------|--------------------------------------------------------------------------------------------|
| ClusterServiceVersion | CSV        | OLM     | application metadata: name, version, icon, required resources, installation, etc...        |
| InstallPlan           | IP         | Catalog | calculated list of resources to be created in order to automatically install/upgrade a CSV |
| CatalogSource         | CS         | Catalog | a repository of CSVs, CRDs, and packages that define an application                        |
| Subscription          | Sub        | Catalog | used to keep CSVs up to date by tracking a channel in a package                            |

Each of these Operators are also responsible for creating resources:

| Operator | Creatable Resources        |
|----------|----------------------------|
| OLM      | Deployment                 |
| OLM      | Service Account            |
| OLM      | Roles                      |
| OLM      | RoleBindings               |
| Catalog  | Custom Resource Definition |
| Catalog  | ClusterServiceVersion   |


## What is a ClusterServiceVersion?

ClusterServiceVersion combines metadata and runtime information about a service that allows OLM to manage it.

ClusterServiceVersion:
 - Metadata (name, description, version, links, labels, icon, etc)
 - Install strategy
    - Type: Deployment
       - Set of service accounts / required permissions
       - Set of deployments
 - CRDs
   - Type
   - Owned - managed by this service
   - Required - must exist in the cluster for this service to run
   - Resources - a list of k8s resources that the Operator interacts with
   - Descriptors - annotate CRD spec and status fields to provide semantic information


## OLM Operator

The OLM Operator is responsible for deploying applications defined by ClusterServiceVersion resources once the required resources specified in the ClusterServiceVersion are present in the cluster.
The OLM Operator is not concerned with the creation of the required resources; users can choose to manually create these resources using `kubectl` or users can choose to create these resources using the Catalog Operator.
This separation of concern enables users incremental buy-in in terms of how much of the OLM framework they choose to leverage for their application.

While the OLM Operator is often configured to watch all namespaces, it can also be operated alongside other OLM Operators so long as they all manage separate namespaces.

### ClusterServiceVersion Control Loop

```
           +------------------------------------------------------+
           |                                                      |
           |                                      +--> Succeeded -+
           v                                      |               |
None --> Pending --> InstallReady --> Installing -|               |
           ^                                       +--> Failed <--+   
           |                                              |
           +----------------------------------------------+
\                                                                 /
 +---------------------------------------------------------------+
    |
    v
Replacing --> Deleting
```

| Phase      | Description                                                                                                                                                                                                                           |
|------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| None       | initial phase, once seen by the Operator, it is immediately transitioned to `Pending`                                                                                                                                                 |
| Pending    | requirements in the CSV are not met, once they are this phase transitions to `Installing`                                                                                                                                             |
| InstallReady | all requirements in the CSV are present, the Operator will begin executing the install strategy                                                                                                                                     |
| Installing | the install strategy is being executed and resources are being created, but not all components are reporting as ready                                                                                                                 |
| Succeeded  | the execution of the Install Strategy was successful; if requirements disappear, or an APIService cert needs to be rotated this may transition back to `Pending`; if an installed component dissapears this may transition to `Failed`|
| Failed     | upon failed execution of the Install Strategy, or an installed component dissapears the CSV transitions to this phase; if the component can be recreated by OLM, this may transition to `Pending`                                     |
| Replacing  | a newer CSV that replaces this one has been discovered in the cluster. This status means the CSV is marked for GC                                                                                                                     | 
| Deleting   | the GC loop has determined this CSV is safe to delete from the cluster. It will disappear soon.                                                                                                                                       |

### Namespace Control Loop

In addition to watching the creation of ClusterServiceVersions in a set of namespaces, the OLM Operator also watches those namespaces themselves.
If a namespace that the OLM Operator is configured to watch is created, the OLM Operator will annotate that namespace with the `alm-manager` key.
This enables dashboards and users of `kubectl` to filter namespaces based on what OLM is managing.

## Catalog Operator

The Catalog Operator is responsible for resolving and installing ClusterServiceVersions and the required resources they specify. It is also responsible for watching catalog sources for updates to packages in channels, and upgrading them (optionally automatically) to the latest available versions.
A user that wishes to track a package in a channel creates a Subscription resource configuring the desired package, channel, and the catalog source from which to pull updates. When updates are found, an appropriate InstallPlan is written into the namespace on behalf of the user.
Users can also create an InstallPlan resource directly, containing the names of the desired ClusterServiceVersions and an approval strategy and the Catalog Operator will create an execution plan for the creation of all of the required resources.
Once approved, the Catalog Operator will create all of the resources in an InstallPlan; this should then independently satisfy the OLM Operator, which will proceed to install the ClusterServiceVersions.

### InstallPlan Control Loop

```
None --> Planning +------>------->------> Installing --> Complete
                  |                       ^
                  v                       |
                  +--> RequiresApproval --+
```

| Phase            | Description                                                                                    |
|------------------|------------------------------------------------------------------------------------------------|
| None             | initial phase, once seen by the Operator, it is immediately transitioned to `Planning`         |
| Planning         | dependencies between resources are being resolved, to be stored in the InstallPlan `Status` |
| RequiresApproval | occurs when using manual approval, will not transition phase until `approved` field is true    |
| Installing       | resolved resources in the InstallPlan `Status` block are being created                      |
| Complete         | all resolved resources in the `Status` block exist                                             |

### Subscription Control Loop

```
None --> UpgradeAvailable --> UpgradePending --> AtLatestKnown -+
         ^                                   |                  |
         |                                   v                  v
         +----------<---------------<--------+---------<--------+
```

| Phase            | Description                                                                                                   |
|------------------|---------------------------------------------------------------------------------------------------------------|
| None             | initial phase, once seen by the Operator, it is immediately transitioned to `UpgradeAvailable`                |
| UpgradeAvailable | catalog contains a CSV which replaces the `status.installedCSV`, but no `InstallPlan` has been created yet |
| UpgradePending   | `InstallPlan` has been created (referenced in `status.installplan`) to install a new CSV                   |
| AtLatestKnown    | `status.installedCSV` matches the latest available CSV in catalog                                             |


## Catalog (Registry) Design

The Catalog Registry stores CSVs and CRDs for creation in a cluster, and stores metadata about packages and channels.

A package manifest is an entry in the catalog registry that associates a package identity with sets of ClusterServiceVersions. Within a package, channels point to a particular CSV. Because CSVs explicitly reference the CSV that they replace, a package manifest provides the catalog Operator all of the information that is required to update a CSV to the latest version in a channel (stepping through each intermediate version).

```
Package {name}
  |
  +-- Channel {name} --> CSV {version} (--> CSV {version - 1} --> ...)
  |
  +-- Channel {name} --> CSV {version}
  |
  +-- Channel {name} --> CSV {version}
```
