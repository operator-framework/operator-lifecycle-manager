# How to contribute

Operator Lifecycle Manager is an Apache 2.0 licensed project and accepts contributions via GitHub pull requests. This document outlines some of the conventions on commit message formatting, contact points for developers, and other resources to help get contributions into the operator-lifecycle-manager project.

## Communication

- Email: [operator-framework][operator_framework]
- Slack: [#olm-dev][olm-dev]
- Working Group: [olm-wg][olm-wg]

## Getting started

- Fork the repository on GitHub
- See the [developer guide](./DEVELOPMENT.md) for build instructions

## Reporting bugs and creating issues

Reporting bugs is one of the best ways to contribute. However, a good bug report has some very specific qualities, so please read over our short document on [reporting bugs](./doc/dev/reporting_bugs.md) before submitting a bug report. Before filing a bug report, ensure the bug hasn't already been reported by searching through the operator-lifecycle-manager project [Issues][issues].

Any new contribution should be accompanied by a new or existing issue. This issue can help track work, discuss the design and implementation, and help avoid wasted efforts or multiple people working on the same issue, compared to submitting a PR first. Trivial changes, like fixing a typo in the documentation, do not require the creation of a new issue. Proposing larger changes to the operator-lifecycle-manager project may require an enhancement be created in the [operator-framework/enhancements](https://github.com/operator-framework/enhancements/) repository.

## Contribution flow

This is a rough outline of what a contributor's workflow looks like:

- Identify or create an issue.
- Create a topic branch from where to base the contribution. This is usually the master branch.
- Make commits of logical units.
- Make sure commit messages are in the proper format (see below).
- Ensure all relevant commit messages contain a valid sign-off message (see below).
- Push changes in a topic branch to a personal fork of the repository.
- Submit a pull request to the operator-framework/operator-lifecycle-manager repository.
- Wait and respond to feedback from the maintainers listed in the OWNERS file.

Thanks for contributing!

### Code Review

Each PR must be labeled with at least one "lgtm" label and at least one "approved" label before it can be merged.

Maintainers that have approval permissions are listed in the "approvers" column in the root [OWNERS][owners] file.

### Code style

The coding style suggested by the Golang community is used in the operator-lifecycle-manager project. See the [style doc](https://github.com/golang/go/wiki/CodeReviewComments) for details.

Please follow this style to make the operator-lifecycle-manager project easier to review, maintain and develop.

### Sign-off ([DCO][DCO])

A [sign-off][sign-off] is a line towards the end of a commit message that certifies the commit author(s).

## Documentation

If the contribution changes the existing APIs or user interface it must include sufficient documentation to explain the use of the new or updated feature.

The operator-lifecycle-manager documentation mainly lives in the [operator-framework/olm-docs][olm-docs] repository.

[operator_framework]: https://groups.google.com/forum/#!forum/operator-framework
[dco]: <https://developercertificate.org/>
[owners]: <https://github.com/operator-framework/operator-lifecycle-manager/blob/master/OWNERS>
[issues]: <https://github.com/operator-framework/operator-lifecycle-manager/issues>
[olm-docs]: <https://github.com/operator-framework/olm-docs>
[olm-dev]: <https://kubernetes.slack.com/archives/C0181L6JYQ2>
[olm-wg]: <https://docs.google.com/document/d/1Zuv-BoNFSwj10_zXPfaS9LWUQUCak2c8l48d0-AhpBw/edit?usp=sharing>
[sign-off]: <https://git-scm.com/docs/git-commit#Documentation/git-commit.txt---signoff>
