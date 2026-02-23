# Steps to create a new release

The OLM project uses [GoReleaser](https://goreleaser.com/) to automatically produce multi-arch container images and release artifacts during the release process.

In order to create a new minor version release, simply create a new tag locally, push that tag up to the upstream repository remote, and let automation handle the rest.

The release automation will be responsible for producing manifest list container images, generating rendered Kubernetes manifests, and a draft release that will contain both of those attached as release artifacts.

## Step 0: Review the Release Milestone

If the release you plan to create corresponds with an existing [milestone](https://github.com/operator-framework/operator-lifecycle-manager/milestones/), make sure that all features have been committed. If a feature will not be added to the release be sure to remove it from the milestone.

## Step 1: Setup the Release Tag

In order to trigger the existing release automation, you need to first create a new tag locally, and push that tag up to the upstream repository remote.

**Note**: The following steps assume that remote is named `origin`.

* Pull the latest.
* Make sure you are on `master` branch.
* Make a new tag that matches the version.
* Push tag directly to this repository.

```bash
# v0.20.0 is the bumped version.
git tag -a v0.20.0 -m "Version 0.20.0"

# origin remote points to operator-framework/operator-lifecycle-manager
git push origin v0.20.0
```

## Step 2: Verify that GoReleaser is Running

Once a manual tag has been created, monitor the progress of the [release workflow action](https://github.com/operator-framework/operator-lifecycle-manager/actions/workflows/goreleaser.yaml) that was triggered when a new tag has been created to ensure a successful run.

Once successful, navigate to the [quay.io/operator-framework/olm image repository](https://quay.io/repository/operator-framework/olm?tab=tags) and ensure that a new manifest list container image has been created.

## Step 3: CHANGELOG Verification

Navigate to the [releases](https://github.com/operator-framework/operator-lifecycle-manager/releases) tab for the OLM repository, and verify that a draft release has been produced and the generated CHANGELOG appears to be correct. GoReleaser is responsible for generating a CHANGELOG based on the diff between the latest tag, and the tag that was just created, excluding some commit(s) that have a `doc` or `test` prefix.

## Step 4: Publish the Draft Release

* Ensure that all links are valid and works as expected.
* Publish the draft release!
