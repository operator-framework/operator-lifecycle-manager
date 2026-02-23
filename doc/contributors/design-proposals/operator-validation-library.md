# Operator Validation Library

### Problem Statement

This project aims to create a validation library within OLM for operators managed by OLM. Such operators are manageable by creating a manifest bundle composed of [ClusterServiceVersions](https://github.com/operator-framework/operator-lifecycle-manager/blob/master/doc/design/building-your-csv.md), [CustomResourceDefinitions](https://kubernetes.io/docs/tasks/access-kubernetes-api/custom-resources/custom-resource-definitions/), and [Package Manifest](https://github.com/operator-framework/operator-lifecycle-manager#discovery-catalogs-and-automated-upgrades) yamls. The steps to creating these files are error-prone and validation of this bundle happens in scattered places that are not always up to date with the latest changes to the manifest&#39;s definition.

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
    
  Ideas: 
  - Include a version of the library alongside the CSV type definition so that it is versioned in the same way the CSV is versioned.
  - To be compatible across different versions of OLM's types and changing requirements/fields within the same version, this library can use the version defined in csv yaml and enforces the field requirements accordingly for validating the manifest.

# Fake README

## Installing

### Command Line Tool

You can interact with this library with a command line tool. 

You can install the `operator-verify` tool from source using:

```
$ go install
```

```bash
$ echo $PATH
```

If you do not have your workspace's `bin` subdirectory in your `$PATH`, 

```bash
$ export PATH=$PATH:$(go env GOPATH)/bin
```

This adds your workspace's bin subdirectory to your PATH. As a result, you can use the `operator-verify` tool anywhere on your system. Otherwise, you would have to `cd` to your workspace's `bin` directory to run the executable. Verify that we can find `operator-verify` in our new `PATH`:

```bash
$ which operator-verify
```

This should return something like:

```
~/go/bin/operator-verify
```

To verify that the library installed correctly, use it to validate the ClusterServiceVersion yaml,

```
$ operator-verify csv /path/to/filename.yaml
```

## Usage

The command line tool's `help` flag gives the following output:

```text
operator-verify is a command line tool for the Operator Manifest Verification Library. This library provides functions to validate the operator manifest bundles against Operator-Lifecycle-Manager's clusterServiceVersion type, customResourceDefinitions, and package yamls. Currently, this application supports static validation of both individual operator files and the manifest bundle. 

Usage:
  operator-verify [flags]
  operator-verify [command]

Available Commands:
  help        Help about any command
  csv         Validate CSV against OLM's type
  crd         Validate manifest CRDs
  package     Validate package yaml
  manifest    Validate operator manifest bundle  

Flags:
  -h, --help    help for operator-verify
  -i, --ignore  ignore warnings in log

Use "operator-verify [command] --help" for more information about a command.
```


### Commands 
**For individual files**: 

```
$ operator-verify csv /path/to/csv.yaml
```

Validates the given CSV yaml file against OLM type and reports errors and warnings as described in the `List of Errors` section below.

Similarly, 

```
$ operator-verify crd /path/to/crd.yaml
```

and 

```
$ operator-verify package /path/to/package.yaml
```

validates CRD and package yaml, respectively. The `crd` command can accept and validate both a single yaml file and a bunch of CRDs at once and report a separate log for each.

**For manifest bundle**:

```
$ operator-verify manifest /path/to/bundle
```

Here `path/to/bundle` is a directory structure as per the [operator manifest format](https://github.com/operator-framework/operator-registry#manifest-format). This command reports errors and/or warnings for each file in the bundle.

Using the `help` flag with `manifest` command we get,

```text
Validates the manifest bundle as a whole, in addition to validating individual operator files. `manifest` reports errors/warnings for each file in the bundle. It works both with a directory structure (as per operator manifest format) and an operator image. `manifest` can also validate only the CSVs or CRDs present in a bundle. See flags for more information. 

Usage:
  operator-verify verify [flags]

Flags:
  -h, --help    help for verify
  -r, --remote  remote manifest (requires operator image)
  --csv-only    validate only bundle CSVs
  --crd-only    validate only bundle CRDs  
```

### Flags

To ignore warnings in the log, we have `-i` flag available, 

```
$ operator-verify -i csv /path/to/csv.yaml
```

This flag works similarly for other commands available in `operator-verify` tool. To validate a remote manifest, we an use the operator image with `-r` flag,

```
$ operator-verify manifest -r <link to operator image>
```

For validating only the CSVs or CRDs in the manifest, we have `--csv-only` and `--crd-only` flags under the `manifest` command.

```
$ operator-verify manifest --csv-only or --crd-only /path/to/bundle
```

We can also use these flags on remote manifest by combining the respective flags.

## Examples

```
$ operator-verify csv csv.yaml
```

Output of the `csv` command against a valid sample csv yaml with some missing optional fields/struct:

```
Warning: Optional Field Missing (ObjectMeta.GenerateName)
Warning: Optional Field Missing (ObjectMeta.SelfLink)
Warning: Optional Field Missing (ObjectMeta.UID)
Warning: Optional Field Missing (ObjectMeta.ResourceVersion)
Warning: Optional Field Missing (ObjectMeta.Generation)
Warning: Optional Struct Missing (ObjectMeta.CreationTimestamp)
Warning: Optional Field Missing (ObjectMeta.DeletionTimestamp)
Warning: Optional Field Missing (ObjectMeta.DeletionGracePeriodSeconds)
Warning: Optional Field Missing (ObjectMeta.Labels)
Warning: Optional Field Missing (ObjectMeta.OwnerReferences)
Warning: Optional Field Missing (ObjectMeta.Initializers)
Warning: Optional Field Missing (ObjectMeta.Finalizers)
Warning: Optional Field Missing (ObjectMeta.ClusterName)
Warning: Optional Field Missing (Spec.CustomResourceDefinitions.Required)
Warning: Optional Struct Missing (Spec.APIServiceDefinitions)
Warning: Optional Field Missing (Spec.NativeAPIs)
Warning: Optional Field Missing (Spec.MinKubeVersion)
Warning: Optional Field Missing (Spec.Provider.URL)
Warning: Optional Field Missing (Spec.Replaces)
Warning: Optional Field Missing (Spec.Annotations)
csv.yaml is verified.
```

Omitting `Spec.InstallStrategy.StrategyName`, one of the mandatory fields, yields

```
Error: Mandatory Field Missing (Spec.InstallStrategy.StrategyName)
Populate all the mandatory fields missing from csv.yaml file.
```

in addition to the warnings shown above.

To ignore the warnings, we have `-i` flag available.

```
$ operator-verify -i csv csv.yaml
```

`crd` and `package` commands work in a similar way as the `csv`. The `manifest` command returns a log similar to the one shown above for `csv` for each file in the manifest bundle. For validating just the CSVs or CRDs in the manifest, we can use the flags mentioned above.

```text
Note: We can have various other APIs for validating only the CSVs or CRDs. For instance,

- `csv`/`crd` command accepts both an individual file or a directory containing a group of respective files. 

   Usage: $operator-verify crd /path/to/directory

- Using a flag for indicating a directory structure. 

   Usage: $operator-verify crd -d /path/to/directory
```

# Library

## Getting Started

The Operator Manifest Verfication library provides APIs for validating both individual yaml files and the manifest bundle as a whole. For **individual yaml files**, it checks for: 

* Data type mismatch
* Missing mandatory and optional fields
* Incompatible configurations
* Logical errors (e.g. business logic)
  
against OLM's type.

For **manifest bundle**, in addition to verifying the individual operator files, this library checks for:

* CRDs mentioned in the CSV
* Incompatible CSV configurations when upgrading to a newer version of CSV

It accepts both nested and flattened directory structures containing manifest yamls. The  directory structure is expected to adhere to [manifest format](https://github.com/operator-framework/operator-registry#manifest-format). See `usage` for more information. 

### List of Errors

* Unmarshalling errors like
  * Data type mismatch 
  * Incorrect indentation
  * Inconsistent yaml file structure that can't be converted to JSON for unmarshalling
* Warning for any missing optional field
* Error for any missing mandatory field
* Error for missing CRDs which are mentioned in the CSV

Errors and warnings returned by the API are of type `missingTypeError` and can be used to extract more information. `missingTypeError` is struct type and its implementation in the library is as follows,

```go
type missingTypeError struct {
	err         string
	typeName    string
	path        string
	isMandatory bool
}
```

For each error/warning, we can check if it's a field or a struct (`typeName`), path of that field in the nested yaml file (`path`), and if the field is mandatory or not (`isMandatory`).
