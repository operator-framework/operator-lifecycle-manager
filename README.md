# ALM
[![Docker Repository on Quay](https://quay.io/repository/coreos/alm/status?token=ccfd2fde-446d-4d82-88a8-4386f8deaab0 "Docker Repository on Quay")](https://quay.io/repository/coreos/alm) [![Docker Repository on Quay](https://quay.io/repository/coreos/catalog/status?token=b5fc43ed-9f5f-408b-961b-c8493e983da5 "Docker Repository on Quay")](https://quay.io/repository/coreos/catalog)

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

* Learn the ALM project [architecture] and [philosophy]
* Follow the [installation guide]
* Understand the YAML resources for the [ALM itself]
* Review the YAML resources for the [existing applications] leveraging the ALM framework
* Learn to [debug] services running with ALM

[architecture]: /Documentation/design/architecture.md
[philosophy]: /Documentation/design/philosophy.md
[debug]: /Documentation/design/debugging.md
[installation guide]: /Documentation/install/install.md
[ALM itself]: /Documentation/design/resources
[existing applications]: /catalog_resources

## Contact

- Slack: #team-apps
- Bugs: [JIRA](https://jira.prod.coreos.systems/projects/ALM/summary)
