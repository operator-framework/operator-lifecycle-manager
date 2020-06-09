// This package defines the valid Operator manifests directory format
// by exposing a set of Validator's to verify a directory and
// its constituent manifests. A manifests directory consists of a
// package manifest and a set of versioned Bundles. Each Bundle contains a
// ClusterServiceVersion and one or more CustomResourceDefinition's.
//
// Errors and warnings, both represented by the Error type, are returned
// by exported functions for missing mandatory and optional fields,
// respectively. Each Error implements the error interface.
//
// Bundle format: https://github.com/operator-framework/operator-registry/#manifest-format
// ClusterServiceVersion documentation: https://github.com/operator-framework/operator-lifecycle-manager/blob/master/Documentation/design/building-your-csv.md
// Package manifest documentation: https://github.com/operator-framework/operator-lifecycle-manager#discovery-catalogs-and-automated-upgrades
// CustomResourceDefinition documentation: https://kubernetes.io/docs/tasks/access-kubernetes-api/custom-resources/custom-resource-definitions/
package validation
