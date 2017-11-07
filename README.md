# ALM

**Note**: The `master` branch may be in an *unstable or even broken state* during development.
Please use [releases] instead of the `master` branch in order to get stable binaries.

[releases]: https://github.com/coreos-inc/alm/releases

![logo-placeholder](https://user-images.githubusercontent.com/343539/30085003-bc6e757c-9262-11e7-86e3-2433b3a884a5.png)

ALM is a project that creates an opinionated framework for managing applications in Kubernetes.

This project enables users to do the following:

* Define applications as a single Kubernetes resource that encapsulates requirements and dashboarding metadata
* Install applications automatically with dependency resolution or manually with nothing but `kubectl`
* Upgrade applications automatically with different approval policies

This project does not:

* Replace [Helm](https://github.com/kubernetes/helm)
* Turn Kubernetes into a [PaaS](https://en.wikipedia.org/wiki/Platform_as_a_service)

## Getting Started

* Learn the ALM project [architecture]
* Follow the [installation guide]
* Understand the YAML resources for the [ALM itself]
* Review the YAML resources for the [existing applications] leveraging the ALM framework

[architecture]: /Documentation/design/architecture.md
[installation guide]: /Documentation/install/install.md
[ALM itself]: /Documentation/design/resources
[existing applications]: /catalog_resources

## Contact

- Slack: #team-apps
- Bugs: [JIRA](https://jira.prod.coreos.systems/projects/ALM/summary)
