# Add service accounts for Operator Groups

Status: Pending

Version: v1 (Operator groups are already v1)

Implementation owner: Abu

## Abstract

Proposal to add a new field for storing a service account to use with Operator Groups in order to ensure permissions for installed operators do not exceed given service account.

## Motivation

In OLM, an operator author writes a ClusterServiceVersion consisting of the required cluster level and namespace level permissions in order to run. Currently those permissions are created by OLM and associated with either the specified service account or one is created. Since OLM has cluster-admin privileges (and must have them) the way permissions are granted now have no bound for what an operator may request.

## Use case

As a cluster administrator, I want to be able to require all operator installs and upgrades to run under a specified service account to ensure no user can install an operator with greater permissions than their own.

## Constraints and assumptions

The provided service account of an Operator Group already has the required permissions for all of the operators to properly run. If the provided service account does not have sufficient permissions, the operator will be prevented from running.

## Declined usage of impersonation

The current state of impersonation upstream does not appear to be very far along at this point. In OpenShift, there exists some helper methods to set the authentication header info based on [passed in parameters](https://github.com/openshift/origin/blob/master/pkg/client/impersonatingclient/impersonate.go), but its usage requires updating the rest client configuration. Porting this to OLM along with modifying the kubernetes client would be required. This approach does not seem that attractive based on it being a lot more complex than simply adding another header.

## Proposal

### Method of authorization

These changes are currently written with the intention of utilizing the RBAC authorizer and the surrounding code in OLM.

### Implementation

The Operator Group spec already has a placeholder for the service account to be specified, so no changes are required to the operator group type itself.

Modify ResolveSteps to have access to the cluster/role listers and the cluster/role binding listers and create a NewCSVRuleChecker that can be passed through NewStepResourceFromBundle -> NewServiceAccountStepResources -> RBACForClusterServiceVersion.

Modify NewServiceAccountStepResources, RBACForClusterServiceVersion to include a service account to be compared against with regard to the service account specified in the CSV. For this proposal, the operator group will be looked up (model from operatorGroupForCSV) to check if a service account has been defined. If a service account is associated with an operator group, permissions in the CSV will be verified to not be greater than the operator group service account (in RBACForClusterServiceVersion). Since an operator may have already been deployed, existing permissions that are owned by the CSV should be cleaned up in this scenario as well.

requirementsAndPermission status should pass the detected service account to NewCSVRuleChecker so that permissionStatus can check the permissions on the correct service account.

Similarly, checkAPIServiceResources needs to be modified to also check permissions if a service account has been specified.