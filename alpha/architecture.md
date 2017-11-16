# Architecture

ALM is composed of two operators: the ALM operator and the Catalog operator.

Each of these operators are responsible for managing the CRDs that are the basis for the ALM framework:

| Resource                 | Short Name | Owner   | Description                                                                                |
|--------------------------|------------|---------|--------------------------------------------------------------------------------------------|
| ClusterServiceVersion-v1 | CSV        | ALM     | application metadata: name, version, icon, required resources, installation, etc...        |
| InstallPlan-v1           | IP         | Catalog | calculated list of resources to be created in order to automatically install/upgrade a CSV |
| AlphaCatalogEntry-v1     | ACE        | Catalog | indexed application metadata for discovery and dependency resolution                       |

Each of these operators are also responsible for creating resources:

| Operator | Creatable Resources        |
|----------|----------------------------|
| ALM      | Deployment                 |
| ALM      | Service Account            |
| ALM      | Roles                      |
| ALM      | RoleBindings               |
| Catalog  | Custom Resource Definition |
| Catalog  | ClusterServiceVersion-v1   |

## ALM Operator

The ALM operator is responsible to deploying applications defined by ClusterServiceVersion-v1 resources once the required resources specified in the ClusterServiceVersion-v1 are present in the cluster.
The ALM operator is not concerned with the creation of the required resources; users can choose to manually create these resources using `kubectl` or users can choose to create these resources using the Catalog operator.
This separation of concern enables users incremental buy-in in terms of how much of the ALM framework they choose to leverage for their application.

While the ALM operator is often configured to watch all namespaces, it can also be operated alongside other ALM operators so long as they all manage separate namespaces.

### ClusterServiceVersion-v1 Control Loop

```
           +-------------------------------------+
           |                                     |
           v                     +--> Succeeded -+
None --> Pending --> Installing -|
                                 +--> Failed
```

| Phase      | Description                                                                                                            |
|------------|------------------------------------------------------------------------------------------------------------------------|
| None       | initial phase, once seen by the operator, it is immediately transitioned to `Pending`                                  |
| Pending    | requirements in the CSV are not met, once they are this phase transitions to `Installing`                              |
| Installing | all required resources are present and the operator is now executing the Install Strategy specified in the CSV         |
| Succeeded  | the execution of the Install Strategy was successful; if requirements disappear, this may transition back to `Pending` |
| Failed     | upon failed execution of the Install Strategy, the CSV transitions to this terminal phase                              |

### Namespace Control Loop

In addition to watching the creation of ClusterServiceVersion-v1s in a set of namespaces, the ALM operator also watches those namespaces themselves.
If a namespace that the ALM operator is configured to watch is created, the ALM operator will annotate that namespace with the `alm-manager` key.
This enables dashboards and users of `kubectl` to filter namespaces based on what ALM is managing.

## Catalog Operator

The Catalog operator is responsible for resolving and installing ClusterServiceVersion-v1s and the required resources they specify.
Users can create an InstallPlan-v1 resource containing the names of the desired ClusterServiceVersion-v1s and an approval strategy and the Catalog operator will create an execution plan for the creation of all of the required resources.
Once approved, the Catalog operator will create all of the resources in an InstallPlan-v1; this should then independently satisfy the ALM operator, which will proceed to install the ClusterServiceVersion-v1s.

### InstallPlan-v1 Control Loop

```
None --> Planning --> Installing --> Complete
```

| Phase      | Description                                                                                    |
|------------|------------------------------------------------------------------------------------------------|
| None       | initial phase, once seen by the operator, it is immediately transitioned to `Planning`         |
| Planning   | dependencies between resources are being resolved, to be stored in the InstallPlan-v1 `Status` |
| Installing | resolved resources in the InstallPlan-v1 `Status` block are being created                      |
| Complete   | all resolved resources in the `Status` block exist                                             |
