# How to contribute

Operator Lifecycle Manager (OLM) is an Apache 2.0 licensed project and accepts contributions via GitHub pull requests (PRs).

This document outlines some of the conventions on commit message formatting, contact points for developers, and other resources to help get contributions into the OLM project.

## Communication

- Email: [operator-framework@googlegroups.com][operator_framework]
- Slack: [#olm-dev][olm-dev]
- Working Group: [olm-wg][olm-wg]

## Getting started

- Fork the repository on GitHub
- See the [developer guide](./DEVELOPMENT.md) for build instructions
- Read the [code of conduct](https://github.com/operator-framework/operator-lifecycle-manager/blob/master/code-of-conduct.md)

## Reporting bugs and creating issues

Reporting bugs is one of the best ways to contribute. However, a good bug report has some very specific qualities, so please read over our short document on [reporting bugs](./doc/dev/reporting_bugs.md) before submitting a bug report. Before filing a bug report, ensure the bug hasn't already been reported by searching through the OLM project [Issues][issues].

Any new contribution should be accompanied by a new or existing issue. This issue can help track work, discuss the design and implementation, and help avoid wasted efforts or multiple people working on the same issue, compared to submitting a PR first. Trivial changes, like fixing a typo in the documentation, do not require the creation of a new issue.

Proposing larger changes to the OLM project may require an enhancement be created in the [operator-framework/enhancements](https://github.com/operator-framework/enhancements/) repository. Enhancements are the primary mechanism for proposing new features to the OLM codebase. Any change to OLM's behavior or existing features, APIs, or architectural changes to the testing harness likely require an enhancement.

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

Contributing PRs with a reasonable title and description can go a long way with helping ease the burden of the review process.

It can be helpful after submitting a PR to self-review your changes. This allows you to communicate sections that reviewers should spend time combing over, asking for feedback on a particular implementation, or providing justification for a set of changes in-line ahead of time.

When opening PRs that are in a rough draft or WIP state, prefix the PR description with `WIP: ...` or create a draft PR. This can help save reviewer's time by communicating the state of a PR ahead of time. Draft/WIP PRs can be a good way to get early feedback from reviewers on the implementation, focusing less on smaller details, and more on the general approach of changes.

When contributing changes that require a new dependency, check whether it's feasable to directly vendor that code [without introducing a new dependency](https://go-proverbs.github.io/).

Each PR must be labeled with at least one "lgtm" label and at least one "approved" label before it can be merged. Maintainers that have approval permissions are listed in the "approvers" column in the root [OWNERS][owners] file.

### Code style

The coding style suggested by the Golang community is used throughout the OLM project:

- CodeReviewComments <https://github.com/golang/go/wiki/CodeReviewComments>
- EffectiveGo: <https://golang.org/doc/effective_go>

In addition to the linked style documentation, OLM formats Golang packages using the [`gofmt`][gofmt] and [`goimports`][goimports] tooling. Before submitting a PR, please run `make lint` locally and commit the results. This will help expedite the review process, focusing less on style conflicts, and more on the design and implementation details.

Please follow this style to make the OLM project easier to review, maintain and develop.

### Sign-off ([DCO][DCO])

A [sign-off][sign-off] is a line towards the end of a commit message that certifies the commit author(s).

For more information on the structuring of commit messages, read the information in the [DCO](https://github.com/apps/dco) application that the OLM projects uses.

## Documentation

If the contribution changes the existing APIs or user interface it must include sufficient documentation to explain the use of the new or updated feature.

The OLM documentation mainly lives in the [operator-framework/olm-docs][olm-docs] repository.

[operator_framework]: https://groups.google.com/forum/#!forum/operator-framework
[dco]: <https://developercertificate.org/>
[owners]: <https://github.com/operator-framework/operator-lifecycle-manager/blob/master/OWNERS>
[issues]: <https://github.com/operator-framework/operator-lifecycle-manager/issues>
[olm-docs]: <https://github.com/operator-framework/olm-docs>
[olm-dev]: <https://kubernetes.slack.com/archives/C0181L6JYQ2>
[olm-wg]: <https://docs.google.com/document/d/1Zuv-BoNFSwj10_zXPfaS9LWUQUCak2c8l48d0-AhpBw/edit?usp=sharing>
[sign-off]: <https://git-scm.com/docs/git-commit#Documentation/git-commit.txt---signoff>
[goimports]: <https://pkg.go.dev/golang.org/x/tools/cmd/goimports>
[gofmt]: <https://pkg.go.dev/cmd/gofmt>
