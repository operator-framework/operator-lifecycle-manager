# Adding Pod Disruption Budgets

## Description

OLM supports users including `PodDisruptionBudget` (PDB) objects in their bundle alongside their operator manifests. `PodDisruptionBudgets`
are used to provide detailed information to the kube-scheduler about how many pods in a collection can be available or unavailable at given time. 
For more info, see the docs at https://kubernetes.io/docs/tasks/run-application/configure-pdb/#protecting-an-application-with-a-poddisruptionbudget. 

## Caveats

PDBs are useful for configuring how many operator replicas or operands should run at any given time. However, it's important
to set reasonable values for any PDBs included in the bundle and carefully consider how the PDB can affect the lifecycle of other resources
in the cluster, such as nodes, to ensure cluster autoscaling and cluster upgrades are able to proceed if they are enabled. 

PDBs are namespaced resources that only affect certain pods selected by the pod selector. However, 
setting `maxUnavailable` to 0 or 0% (or `minAvailable` to 100%) on the PDB means zero voluntary evictions. 
This can make a node impossible to drain and block important lifecycle actions like operator upgrades or even cluster upgrades. 

Multiple PDBs can exist in one namespace- this can cause conflicts. For example, a PDB with the same name may already exist in the namespace.
PDBs should target a unique collection of pods and not overlap with existing pods in the namespace. 
Be sure to know of existing PDBs in the namespace in which your operator and operands will exist in the cluster. 

PDBs for pods controlled by operators have additional restrictions around them. See https://kubernetes.io/docs/tasks/run-application/configure-pdb/#arbitrary-controllers-and-selectors
for additional details - PDBs for operands managed by OLM-installed operators will fall into these restrictions. 

## Technical Details

PDB yaml manifests can be placed in the bundle alongside existing manifests in the `/manifests` directory. The PDB manifest will be stored 
in the bundle image. 

When OLM attempts to install the bundle, it will see the PDB and create it on-cluster. Since PDBs are namespace-scoped resources, 
it will be created in the same namespace as the `InstallPlan` associated with the operator. The PDB will be visible in the `InstallPlan`
and if the PDB fails to be installed OLM will provide a descriptive error in the `InstallPlan`. 

OLM installs additional objects in the bundle after installing the CRDs and the CSV, to ensure proper owner references between the objects
and the CSV. Therefore, there may be an initial period where additional objects are not available to the operator. 

When the operator is removed, the PDB will be removed as well via the kubernetes garbage collector. The PDB will be updated when installing a newer version of the operator - 
the existing PDB will be updated to the new PDB on-cluster. An upgrade to an operator bundle which does not include a PDB will remove the existing PDB from the cluster. 

Prior versions of OLM (pre-0.16.0) do not support PDBs. If a PDB is present in a bundle attempting to be installed on-cluster, OLM will throw an invalid installplan error
specifying that the resource is unsupported. 

## Limitations on Pod Disruption Budgets

No limitations are placed on the contents of a PDB at this time when installing on-cluster, but that may change as OLM develops
an advanced strategy to ensure installed objects do not compromise the cluster. 

However, the following are suggested guidelines to follow when including PDB objects in a bundle. 

* `maxUnavailable` field cannot be set to 0 or 0%. 
    * This can make a node impossible to drain and block important lifecycle actions like operator upgrades or even cluster upgrades.
* `minAvailable` field cannot be set to 100%.
    * This can make a node impossible to drain and block important lifecycle actions like operator upgrades or even cluster upgrades.
