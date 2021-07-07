# Architecture

OLM is composed of two Operators: the OLM Operator and the Catalog Operator.

Each of these Operators is responsible for managing the CRDs that are the basis for the OLM framework:

| Resource                 | Short Name | Owner   | Description                                                                                |
|--------------------------|------------|---------|--------------------------------------------------------------------------------------------|
| ClusterServiceVersion | csv        | OLM     | application metadata: name, version, icon, required resources, installation, etc...        |
| InstallPlan           | ip         | Catalog | calculated list of resources to be created in order to automatically install/upgrade a CSV |
| CatalogSource         | catsrc         | Catalog | a repository of CSVs, CRDs, and packages that define an application                        |
| Subscription          | sub        | Catalog | used to keep CSVs up to date by tracking a channel in a package                            |
| OperatorGroup         | og     | OLM     | used to group multiple namespaces and prepare for use by an operator                     |

Each of these Operators are also responsible for creating resources:

| Operator | Creatable Resources        |
|----------|----------------------------|
| OLM      | Deployment                 |
| OLM      | Service Account            |
| OLM      | (Cluster)Roles             |
| OLM      | (Cluster)RoleBindings      |
| Catalog  | Custom Resource Definition |
| Catalog  | ClusterServiceVersion      |

## What is a ClusterServiceVersion

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

The OLM Operator is responsible for installing applications defined by a `ClusterServiceVersion` resources once the required resources specified in the CSV are present in the cluster (the job of the Cluster Operator). This may be as simple as setting up a single `Deployment` resulting in an operator's pod becoming available. The OLM Operator is not concerned with the creation of the underlying resources. If this is not done manually, the Catalog Operator can help provide resolution of these needs.

This separation of concerns enables users incremental buy-in of the OLM framework components. Users can choose to manually create these resources, or define an InstallPlan for the Catalog Operator or allow the Catalog Operator to develop and implement the InstallPlan. An operator creator does not need to learn about the full operator package system before seeing a working operator.

While the OLM Operator is often configured to watch all namespaces, it can also be operated alongside other OLM Operators so long as they all manage separate namespaces defined by `OperatorGroups`.

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
| Succeeded  | the execution of the Install Strategy was successful; if requirements disappear, or an APIService cert needs to be rotated this may transition back to `Pending`; if an installed component disappears this may transition to `Failed`|
| Failed     | upon failed execution of the Install Strategy, or an installed component disappears the CSV transitions to this phase; if the component can be recreated by OLM, this may transition to `Pending`                                     |
| Replacing  | a newer CSV that replaces this one has been discovered in the cluster. This status means the CSV is marked for GC                                                                                                                     |
| Deleting   | the GC loop has determined this CSV is safe to delete from the cluster. It will disappear soon.                                                                                                                                       |
> Note: In order to transition, a CSV must first be an active member of an OperatorGroup

## Catalog Operator

The Catalog Operator is responsible for monitoring `Subscriptions`, `CatalogSources` and the catalogs themselves. When it finds a new or changed `Subscription`, it builds out the subscribed Operator. When it finds a new or changed CatalogSource it builds out the required catalog, if appropriate, and begins regular monitoring of the package in the catalog. The packages in the catalog will include various `ClusterServiceVersions`, `CustomResourceDefinitions` and a channel list for each package. A catalog has packages. A package has channels and CSVs. A Channels identifies a specific CSV. The CSVs identify specific CRDs.

A user wanting a specific operator creates a `Subscription` which identifies a catalog, operator and channel within that operator. The Catalog Operator then receives that information and queries the catalog for the latest version of the channel requested. Then it looks up the appropriate `ClusterServiceVersion` identified by the channel and turns that into an `InstallPlan`. When updates are found in the catalog for the channel, a similar process occurs resulting in a new `InstallPlan`. (Users can also create an InstallPlan resource directly, containing the names of the desired ClusterServiceVersions and an approval strategy.) 

When the Catalog Operator find a new `InstallPlan`, even though it likely created it, it will create an "execution plan" and embed that into the `InstallPlan` to create all of the required resources. Once approved, whether manually or automatically, the Catalog Operator will implement its portion of the the execution plan, satisfying the underlying expectations of the OLM Operator. 

The OLM Operator will then pickup the installation and carry it through to completion of everything required in the identified CSV.

### InstallPlan Control Loop

```
None --> Planning +------>------->------> Installing +---> Complete
                  |                       ^          |
                  v                       |          v
                  +--> RequiresApproval --+          Failed
```

| Phase            | Description                                                                                    |
|------------------|------------------------------------------------------------------------------------------------|
| None             | initial phase, once seen by the Operator, it is immediately transitioned to `Planning`         |
| Planning         | dependencies between resources are being resolved, to be stored in the InstallPlan `Status`    |
| RequiresApproval | occurs when using manual approval, will not transition phase until `approved` field is true    |
| Installing       | waiting for reconciliation of required resource(OG/SA etc), or resolved resources in the 
                     InstallPlan `Status` block are being created                                                   |
| Complete         | all resolved resources in the `Status` block exist                                             |
| Failed           | occurs when resources fail to install or when bundle unpacking fails                           |

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
