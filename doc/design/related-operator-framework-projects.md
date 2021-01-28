# Related Operator Framework Projects

There are a number of projects that enable OLM to install and manage the lifecycle of an operator. Each of these projects have specific goals and their own GitHub repository. Due to the distributed nature of these projects, it can be difficult to know where a change must be made when introducing a new feature or bug fix to OLM.

It is the goal of this document to keep track of the various projects which enable OLM to install and manage the lifecycle of operators.

## Operator SDK

The [Operator SDK][sdk] is used by developers to create an operator. This project is closely aligned with [Kubebuilder][kubebuilder] but offers additional support for building operators shipped and managed by OLM. Some of these support features include:

- Generating a ClusterServiceVersion
- Generating an Operator Bundle
- Generating an Operator Index

As a developer, you may need to make a change to this repository if you wish to:

- Fix an issue for an operator built with the SDK
- Fix an issue where an operator built with the SDK is incompatible with OLM
- Introduce a new feature that makes it easier for operator authors to integrate with OLM

## Operator Registry

The [operator-framework/operator-registry][registry] project enables Operator Author to ship versions of their operators to clusters with OLM installed. This project focuses on two resources:

1. [Bundles](https://github.com/operator-framework/operator-registry/blob/master/docs/design/operator-bundle.md) which are an image that contains both the metadata and manifests for a specific version of an operator.
2. [Indexes](https://github.com/operator-framework/operator-registry#manifest-format) which are an image that contains a database that serves a collection of bundles. Indexes may contain a collection of different operators and their various versions. These indexes are then used by OLM as `CatalogSources` to make new operator content available on cluster.

This repository also includes the [opm](https://github.com/operator-framework/operator-registry#building-an-index-of-operators-using-opm) command line tool which generates and updates registry databases as well as the index images that encapsulate them.

As a developer, you may need to make a change to this repository if you wish to modify how operator content is packaged for use with OLM.

## API

The APIs defined by the Operator Framework toolkit can be found at the [operator-framework/API][api] GitHub Repository. These APIs are typically surfaced and consumed by users as CustomResourceDefinitions. The static validation logic for ClusterServiceVersions can also be found in the [validation package](https://github.com/operator-framework/api/tree/master/pkg/validation).

As a developer. you may need to make a PR against this repo when you need to introduce a new API or modify an existing API when working on a new feature or implementing a bug fix. Examples of APIs stored in this repo include:

- ClusterServiceVersion
- OperatorConditions
- CatalogSources
- OperatorGroups
- Subscriptions
- InstallPlans
- Operators

When changing an API you should:

1. Fork the [API repo][api] and create a new branch. This project uses [Go Modules](https://blog.golang.org/using-go-modules) and does not need to be cloned inside the `$GOPATH` provided that your installed version of GO is greater than or equal to 1.11.
2. Making the intended changes to the *_type.go file found in the [./pkg/operators][https://github.com/operator-framework/api/tree/master/pkg/operators] directory
3. Run the `make generate` command to generate generated code
4. Run the `make manifests` command to generate/update the CustomResourceDefinitions to include the changes you made
5. Test the changes you made locally with OLM
6. Make a PR against the API Repo and request feedback
7. Make a new release version of the API
8. Vendor this version of the API into OLM

[sdk]: https://github.com/operator-framework/operator-sdk
[registry]: https://github.com/operator-framework/operator-registry
[api]: https://github.com/operator-framework/api
[kubebuilder]: https://github.com/kubernetes-sigs/kubebuilder
