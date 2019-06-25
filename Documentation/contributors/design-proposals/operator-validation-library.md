# Operator Validation Library

### Problem Statement

This project aims to create a validation library within OLM for operators managed by OLM. Such operators are manageable by creating a manifest bundle composed of [ClusterServiceVersions](https://github.com/operator-framework/operator-lifecycle-manager/blob/master/Documentation/design/building-your-csv.md), [CustomResourceDefinitions](https://kubernetes.io/docs/tasks/access-kubernetes-api/custom-resources/custom-resource-definitions/), and [Package Manifest](https://github.com/operator-framework/operator-lifecycle-manager#discovery-catalogs-and-automated-upgrades) yamls. The steps to creating these files are error-prone and validation of this bundle happens in scattered places that are not always up to date with the latest changes to the manifest&#39;s definition.

For example, operator-sdk&#39;s [csv-gen](https://github.com/operator-framework/operator-sdk/blob/master/doc/user/olm-catalog/generating-a-csv.md) tool uses static verification logic based on OLM&#39;s CSV type to validate the generated file. There is also [scorecard](https://github.com/operator-framework/operator-sdk/blob/master/doc/test-framework/scorecard.md) that uses runtime checks to validate the bundle. [operator-courier](https://github.com/operator-framework/operator-courier) uses static verification logic before pushing the bundles to an app-registry. Finally, the [operator-registry](https://github.com/operator-framework/operator-registry) and [olm-operator](https://github.com/operator-framework/operator-lifecycle-manager/tree/master/pkg/controller/operators/olm) both contain logic for static and runtime validation of CSVs.

Each of these validates only a piece of the bundle and most are not in sync with changes to the CSV type. This means operator owners use different tools to validate their bundle - each providing partial or inconsistent validation - which dampens the user experience. The operator framework needs to agree on a single source of truth for a valid OLM-managed operator.

Our plan is to bring the already defined validators scattered across various code bases (along with potentially new validators) into a single location/package with validation logic that adapts to changes made to OLM.

### Stakeholders

Our main stakeholders of this library are:

- OLM/operator-registry: refactoring some code to call a single validation library may help with improving user experience.
- Operator-sdk: calling this library for the csv-gen tool instead of creating custom functions.
- Operator-courier (or similar tool): community-operators or upstream operator owners who are not using the sdk to build their operator need a validation tool.
- OSD SRE: validating bundles before pushing them down their deployment pipeline to minimize resource expenditure.

### Development Strategy

As a preliminary step, this library is interested in static verification of operator bundles. Static verification is crucial to help improve the development process of operators. It is time efficient and can be used in a variety of contexts: from IDE linter extensions to CI pipelines with limited resources to command line utilities. All consistent by calling the same APIs from this library.

In the future, we aim to extend this library to contain runtime tests and apis to be called by runtime tools on cluster.

### Requirements

- Methods of this library should adapt to any changes in OLM&#39;s type. There should be minimal to no changes required when there is a change in OLM&#39;s type and/or version. Or if this is not possible, methods of the library need to be part of the build/test pipeline of OLM.
- Better error reporting by informing the user of all errors found instead of exiting after the first occurrence. We should define custom error types easily digestible by users of these APIs.
- APIs should expose verification methods for individual yaml files as well as for the bundle as a whole.
- This library should expose a validator interface for users to define their own custom validators (ex: operatorhub.io specific validators) while obeying the same overarching structure.

### Progress so far

[https://github.com/dweepgogia/new-manifest-verification](https://github.com/dweepgogia/new-manifest-verification)

We have started with the validation of Cluster Service Version yaml files. Currently, this library checks for the following against OLM&#39;s CSV type:

- Data type mismatch.
- Mandatory/optional field/struct/value missing (reports errors and warnings for mandatory and optional fields, respectively). Currently, this uses the json annotation `omitempty` to determine whether the field is optional.

There is also a Command Line Tool that serves as an example of how a user can interact with the library. Details on using the CLI tool can be found in the `README` of the repo above.

### Outcomes

- A universally accepted definition of operator validation tests.
- Improved developer experience brought about through the reduction of the number of tools required to build, test, and distribute an operator.
- Stronger alignment across operator-framework teams.

### Open Questions

- How do we deal with different versions of the CSV type?
  - Ideas: include a version of the library alongside the CSV type definition so that it is versioned in the same way the CSV is versioned.
