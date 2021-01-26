# How to contribute

Operator Lifecycle Manager is Apache 2.0 licensed and accepts contributions via GitHub pull requests. This document outlines some of the conventions on commit message formatting, contact points for developers, and other resources to help get contributions into operator-lifecycle-manager.

## Communication

- Slack: [#olm-dev on the Kubernetes Slack](https://kubernetes.slack.com/archives/C0181L6JYQ2)
- Google Group: [OLM Dev Google Group](https://groups.google.com/u/1/g/operator-framework-olm-dev)
- Mailing List: [operator-framework-olm-dev@googlegroups.com](operator-framework-olm-dev@googlegroups.com)
- Scheduled upstream meetings can be found at the [operator-framework/community GitHub Repository][community]

> Note: By joining the Google Group you will automatically be added to the mailing list.

## Getting started

- Fork and clone the repository on GitHub.
  -  This project uses [Go Modules](https://blog.golang.org/using-go-modules) and does not need to be cloned inside the `$GOPATH` provided that your installed version of GO is greater than or equal to 1.11.
- Review the design documentation in the [doc/design](./doc/design) directory
- Watch the content on the [Operator Framework Youtube Channel][of-youtube]
- See the [developer guide](./DEVELOPMENT.md) for build instructions
- Join the existing [Communication][communication] channels and introduce yourself to the other OLM Developers

## Reporting bugs and creating issues

If any part of the operator-lifecycle-manager project has bugs or documentation mistakes, please let us know by [opening an issue][operator-olm-issue]. We treat bugs very seriously and believe no issue is too small. Before creating a bug report, please check that an issue reporting the same problem does not already exist. If you aren't sure if the observed behavior is a bug, please reach out to the OLM team using the links provided under [communication][communication].

To make the bug report accurate and easy to understand, please try to create bug reports that are:

- Specific. Include as much detail as possible: which version, what environment, what configuration, etc.

- Reproducible. Include the steps to reproduce the problem. We understand some issues might be hard to reproduce, please include the steps that might lead to the problem.

- Isolated. Please try to isolate and reproduce the bug with minimum dependencies. It would significantly slow down the speed to fix a bug if too many dependencies are involved in a bug report. Debugging external systems that rely on operator-lifecycle-manager is out of scope, but we are happy to provide guidance in the right direction or help with using operator-lifecycle-manager itself.

- Unique. Do not duplicate existing bug report.

- Scoped. One bug per report. Do not follow up with another bug inside one report.

It may be worthwhile to read [Elika Etemad’s article on filing good bug reports][filing-good-bugs] before creating a bug report.

We might ask for further information to locate a bug. A duplicated bug report will be closed.

## Contribution flow

This is a rough outline of what a contributor's workflow looks like:

- Create a topic branch from where to base the contribution. This is usually master.
- Make commits of logical units using the `--signoff` flag.
- Push changes in a topic branch to a personal fork of the repository.
- Submit a pull request to operator-framework/operator-lifecycle-manager.
- The pull request must then be reviewed by members of the operator framework team. Please notify the OLM Development team when you create a pull request using one of the links provided under [communication][communication]. As your PR is reviewed, please attempt to keep any design discussions within the pull request. If you have responded to all the messages on your PR and are concerned that it might have been forgotten, please ping us once again using one of the links provided under  [communication][communication]. Your PR will merge once:
  - The PR has received a `LGTM` from a reviewer found in the OWNERS file.
  - The PR has received an `APPROVAL` from a approver found in the OWNERS file.
  - The PR has received an `okay-to-test` label from a member of the [operator-framework](https://github.com/orgs/operator-framework/people) organization, allowing the test suite to run.
  - All test suites have passed

Thanks for contributing!

### Code style

The coding style suggested by the Golang community is used in operator-lifecycle-manager. See the [style doc](https://github.com/golang/go/wiki/CodeReviewComments) for details.

Please follow this style to make operator-lifecycle-manager easy to review, maintain and develop.

## Documentation

If the contribution changes the existing APIs or user interface it must include sufficient documentation to explain the use of the new or updated feature. Likewise the [CHANGELOG][changelog] should be updated with a summary of the change and link to the pull request.

[operator_framework]: https://groups.google.com/forum/#!forum/operator-framework
[changelog]: https://github.com/operator-framework/operator-lifecycle-manager/blob/master/CHANGELOG.md
[operator-olm-issue]: https://github.com/operator-framework/operator-lifecycle-manager/issues/new
[filing-good-bugs]: http://fantasai.inkedblade.net/style/talks/filing-good-bugs/
[communication]: #communication
[community]: https://github.com/operator-framework/community/blob/master/README.md
[of-youtube]: https://www.youtube.com/channel/UCxRfXpCVxnotoSGxEpB1hwA
