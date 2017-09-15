# ALM

**Note**: The `master` branch may be in an *unstable or even broken state* during development.
Please use [releases] instead of the `master` branch in order to get stable binaries.

[releases]: https://github.com/coreos-inc/alm/releases

![logo-placeholder](https://user-images.githubusercontent.com/343539/30085003-bc6e757c-9262-11e7-86e3-2433b3a884a5.png)

ALM is a project that creates an opinionated framework for managing the overall lifecycle of applications in Kubernetes.

This enables Tectonic users to do the following in a Kubernetes-native way:

* leverage a catalog for discovery and installation of applications across their namespaces
* automatically upgrade between compatible versions of applications and their operators
* relate application resources together using well defined inputs and outputs

## Getting Started

* Follow the [installation guide]
* Read the [original design proposal]
* Checkout some mocks for the [Tectonic Console integration]
* Review the developing YAML resources for the [ALM itself]
* Review some YAML resources for [sample operators] using ALM

[installation guide]: /Documentation/install.md
[original design proposal]: /Documentation/design/original-proposal.md
[Tectonic Console integration]: /Documentation/design/mocks
[ALM itself]: /Documentation/design/resources
[sample operators]: /Documentation/design/resources/samples

## Contact

- Slack: #team-apps
- Bugs: [JIRA](https://coreosdev.atlassian.net/projects/ALM/summary)
