# Access Control Philosophy

The [architecture][arch] is designed around a number of CRDs that ensure that the main personas of your clusters, the cluster admins and end users, have the appropriate permissions to get their jobs done while maintaining a degree of access control.

Using CRDs for this allows for default roles to be modeled using Kubernetes RBAC, which integrates into the wide variety of community tools like `kubectl` as well as the API server's audit log.

## End Users

End users are the engineers, operations and manager staff that utilize the cluster to run applications. OLM is designed to facilitate the installation and management of Operator instances in a self-service manner within a namespace.

Running an Operator manually requires access to cluster-level permissions, which end users don't typically have. Here’s a typical list of tasks required:

1. Create Service Account for Operator
1. Create minimal Role for the Operator
1. Create Role Binding for Role and Service Account
1. Create the Custom Resource Definition
1. Create Operator Deployment, referencing the Service Account
1. Create an instance of the custom resource within a namespace
1. Operator uses Service Account to create the app resources (Deployments, Pods, etc)

In order to both ensure self-service _and_ minimal permissions, OLM generates these cluster-level resources on behalf of the end user, in a manner that is safe and auditable. Once an admin has installed/granted access to an Operator (see below), the end user only needs to:

1. Create an instance of the custom resource within a namespace
1. Operator uses Service Account to create the app resources (Deployments, Pods, etc)

As you can see, no cluster permissions are needed.

## Cluster Admins

Cluster admins have the ability to provide a selection of Operators for use on the cluster. These Operators are described in a Cluster Service Version (CSV) file which resides in a CatalogSource (along with the Operator's CRD and package manifests). The cluster admin can now select the teams and namespaces that can use this particular Operator, by creating a Subscription object, which will trigger the creation of an InstallPlan that points to a specific CatalogSource. Once the InstallPlan is approved, the OLM software is responsible for parsing the CatalogSource and performing the following:

1. Create the Custom Resource Definition
1. Create Service Account for Operator
1. Create minimal Role or ClusterRole for the Operator
1. Create Role or ClusterRoleBinding for Role or ClusterRole and Service Account
1. Create Operator Deployment, referencing the Service Account

Once a namespace is created, the end-users now have the ability to create instances of the Custom Resource in a self-service manner (see above). OLM also has the ability to control automatic updates of the Operators running in namespaces. See the [architecture][arch] for more details.

## Invent Your Own Personas

OLM uses standard Kubernetes RBAC so that admins can create customized personas in addition to the methods described above. For example, if you want to allow a larger group of namespace admins to subscribe to various Operators without being a cluster admin, they can be granted access to CRUD on Subscription objects.

If you want your end-users to be able to install CSVs themselves, they can be granted access to CSVs and Subscriptions. This is typically done when you are producing Operators as part of your product or internal platform.

[arch]: architecture.md
