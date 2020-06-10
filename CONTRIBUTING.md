# How to contribute

Operator Lifecycle Manager is Apache 2.0 licensed and accepts contributions via GitHub pull requests. This document outlines some of the conventions on commit message formatting, contact points for developers, and other resources to help get contributions into operator-lifecycle-manager.

# Email and Chat

- Email: [operator-framework][operator_framework]  

## Getting started

- Fork the repository on GitHub
- See the [developer guide](./DEVELOPMENT.md) for build instructions

## Reporting bugs and creating issues

Reporting bugs is one of the best ways to contribute. However, a good bug report has some very specific qualities, so please read over our short document on [reporting bugs](./doc/dev/reporting_bugs.md) before submitting a bug report. This document might contain links to known issues, another good reason to take a look there before reporting a bug.

## Contribution flow

This is a rough outline of what a contributor's workflow looks like:

- Create a topic branch from where to base the contribution. This is usually master.
- Make commits of logical units.
- Make sure commit messages are in the proper format (see below).
- Push changes in a topic branch to a personal fork of the repository.
- Submit a pull request to operator-framework/operator-lifecycle-manager.
- The PR must receive a LGTM from two maintainers found in the MAINTAINERS file.

Thanks for contributing!

### Code style

The coding style suggested by the Golang community is used in operator-lifecycle-manager. See the [style doc](https://github.com/golang/go/wiki/CodeReviewComments) for details.

Please follow this style to make operator-lifecycle-manager easy to review, maintain and develop.

## Documentation

If the contribution changes the existing APIs or user interface it must include sufficient documentation to explain the use of the new or updated feature. Likewise the [CHANGELOG][changelog] should be updated with a summary of the change and link to the pull request.


[operator_framework]: https://groups.google.com/forum/#!forum/operator-framework
[changelog]: https://github.com/operator-framework/operator-lifecycle-manager/blob/master/CHANGELOG.md
