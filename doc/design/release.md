# Steps to create a new release

## Step 0: Review the Release Milestone

If the release you plan to create corresponds with an existing [milestone](https://github.com/operator-framework/operator-lifecycle-manager/milestones/), make sure that all features have been committed. If a feature will not be added to the release be sure to remove it from the milestone.

## Step 1: Installing Requirements

Ensure you have `autoconf`, `automake`, and `libtool` installed.

The following command can be used to install these packages on Fedora:

```bash
dnf install autoconf automake libtool
```

## Step 2: Verify Manifests

* Make sure you have a clean workspace. `git status` should show no change(s) or untracked file.
* Make sure you pull the latest from `upstream`.
* Checkout `master` branch.
* Run `make release`

## Step 3: Bump the Version

* Bump the version in `OLM_VERSION` file. Make a new PR with this change only.
* Wait until the PR has been merged.

## Step 4: Setup Tag

If git `push` is disabled on `upstream` repository in your fork, then clone this repository so that you can push to `master` directly.

* Pull the latest.
* Make sure you are on `master` branch.
* Make a new tag that matches the version.
* Push tag directly to this repository.

```bash
# 0.11.0 is the bumped version.
git tag -a v0.18.0 -m "Version 0.18.0"

# origin remote points to operator-framework/operator-lifecycle-manager
git push origin v0.18.0
```

* Confirm that new images have been built here: <https://quay.io/repository/operator-framework/olm?tab=builds>.

## Step 5: Generate Manifests

* Make sure you have a clean workspace. `git status` should show no change(s) or untracked file.
* Make sure you pull the latest from `upstream`.
* Run `make release` on `master` branch.
* Make a new PR and ensure all tests pass for merging.

Verify the following:

* The image digest in manifest file(s) matches the new tag in `quay.io`.

## Step 6: Generate Changelog

Changelogs for OLM are generated using [GitHub Changelog Generator](https://github.com/github-changelog-generator/github-changelog-generator).

You need to have `gem` installed on your workstation. Execute the following command to install `github-changelog-generator`.

```bash
gem install github_changelog_generator
```

Make sure you have a GitHub API access token. You can generate one from [tokens](https://github.com/settings/tokens)

Run the following command to generate a changelog for this release:

```bash
# <start-semver> is the previous version.
# <end-semver> is the new release you have made.
GITHUB_TOKEN="<github-api-token>"
github_changelog_generator \
    -u operator-framework \
    -p operator-lifecycle-manager \
    --token=${GITHUB_TOKEN} \
    --since-tag=<start-semver> \
    --future-release=<end-semver> \
    --pr-label="**Other changes:**" \
    -b CHANGELOG.md
```

**Note**: You may run into API rate limiting when attempting to generate the changelog.

By default, all open and closed issues will be queried when running the above command, which can lead to API rate limiting. Lower the number of issues that will be queried by specifying the `--max-issues` flag (e.g. `--max-issues=100`) and re-run the above command with that flag provided.

Ensure the `CHANGELOG.md` has been modified locally and open a PR with those generated changes.

## Step 7: Create a New GitHub Release

* Create a new GtiHub release [here](https://github.com/operator-framework/operator-lifecycle-manager/releases/new)
* Choose the new tag matching the version you created.
* You can set `Title` to the same value as the tag name.
* Add the generated `changelog` to the release description.
* Save `draft` of the release.

## Step 8: QuickStart

Edit the GitHub Release:

* Upload the files `crds.yam`, `install.sh` and `olm.yaml` as release artifacts. These files are located in `deploy/upstream/quickstart`
* Add install instruction, see an [example here](https://github.com/operator-framework/operator-lifecycle-manager/releases/tag/0.10.0#Install).

## Step 9: Publish Release

* Ensure that all links are valid and works as expected.
* Publish the release!
