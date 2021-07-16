# Release Governance

The goal of this document is to define the method and schedule of Operator Lifecycle Manager releases.

## Scope

- Define a consistent release cadence for the project
- Provide a framework to ensure consistent high quality releases
- Define a consistent understanding of what is in scope for a release
- Ensure community members understand the release cycle

## Roles and Organization

The Operator Lifecycle Manager project is part of the Operator Framework. As a result, it's community membership guidelines adhere to the definitions defined in the Operator Framework's [community membership definition](https://github.com/operator-framework/community/blob/master/community-membership.md). For the purpose of this document, there are two roles that are relevant for the release schedule.

- Project reviewers provide guidance on whether or not a release is ready. Before a release can be cut, we will require that two reviewers provide the `/lgtm` label to the release.
- Project approvers provide the final approval on whether or not a release is ready to be cut. A quorum (greater than 50%) of the project's approvers need to agree that a release is ready by writing the `/approve` label to the release.

The set of reviewers and approvers is defined by the project's [OWNERS file](https://github.com/operator-framework/operator-lifecycle-manager/blob/master/OWNERS)

## Release Process

### Minor Releases
When a minor release is ready to be generated, a github issue will be filed against the project containing preliminary release notes. At that time, it should be labeled with the milestone expected for that release. Once that issue is opened, it needs approval from two reviewers and a majority of project approvers before the release can be scheduled. Additionally, at the same time, an email will be sent out to the operator framework mailing list with a link to the release notes in the issue. At that time, there is a minimum of a 48 hour comment period before the release can be completed.

### Patch Releases
A patch release has less strict requirements. In this case, the same process as a minor release will be followed with the exception of waiving the 48 hour comment period, as a patch release generally implies no breaking changes and should be only for fixing existing issues.

## Release Content
The project approvers also define what changes should be planned for a given release. The biweekly planning meeting is the forum where issues are groomed and define what is currently on track for a given release. When a given issue is agreed upon by the project approvers, it will be labeled with a milestone tag for the given minor release.

- Note: As part of the release, all issues marked with that milestone must be completed. If there is an issue that is marked for a given milestone, then a release cannot be scheduled unless the project approvers agree to remove that milestone tag from the issue.

## Release Schedule

### Cadence
The high level objective for Operator Lifecycle Manager releases is to release a minor version on a 3 month cadence

- Minor releases alongside the current Kubernetes release schedule
  - Minor version increments should coincide with a kubernetes minor release
  - Timing of the release should follow along but not necessarily immediately match Kuberenetes release scheduling

## Release Support
Minor releases currently do not have any support guarantees on top of them. While the project may agree to increment the patch version in order to fix any outstanding issues with the latest release, there are no guarantees that a non latest version will be patched with a fix from a later version.

In the event that a true breaking issue is found that prevents the existing version from working, the project approvers may vote to pull a patch down to an existing minor version and cut a new patch release.
