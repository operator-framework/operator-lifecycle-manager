# Architecture

## Operators and their Resources

ALM is composed of two operators: the ALM operator and the Catalog operator.

Each of these operators is responsible for managing the CRDs that are the basis for the ALM framework:

| Resource                 | Short Name | Owner   | Description                                                                                |
|--------------------------|------------|---------|--------------------------------------------------------------------------------------------|
| ClusterServiceVersion-v1 | CSV        | ALM     | application metadata: name, version, icon, required resources, installation, etc...        |
| InstallPlan-v1           | IP         | Catalog | calculated list of resources to be created in order to automatically install/upgrade a CSV |
| AlphaCatalogEntry-v1     | ACE        | Catalog | indexed application metadata for discovery and dependency resolution                       |

The ALM operator is responsible to deploying applications defined by ClusterServiceVersion-v1 resources once the required resources specified in the ClusterServiceVersion-v1 are present in the cluster.
The ALM operator is not concerned with the creation of the required resources; users can choose to manually create these resources using `kubectl` or users can choose to create these resources using the Catalog operator.
This separation of concern enables users incremental buy-in in terms of how much of the ALM framework they choose to leverage for their application.

The Catalog operator is responsible for resolving and installing ClusterServiceVersion-v1s and the required resources they specify.
Users can create an InstallPlan-v1 resource containing the names of the desired ClusterServiceVersion-v1s and an approval strategy and the Catalog operator will create an execution plan for the creation of all of the required resources by referencing the available AlphaCatalogEntry-v1s.
Once approved, the Catalog operator will create all of the resources in an InstallPlan-v1; this should then independently satisfy the ALM operator, which will proceed to install the ClusterServiceVersion-v1s.

## Control Loops

TODO(jzelinskie): detailed analysis of the control loops for each operator, such that details such as annotations etc... is made clear
