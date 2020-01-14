# Steps to create a new release

## Step 0: Installing Requirements

Ensure you have `autoconf`, `automake`, and `libtool` installed. On Fedora, you can run

```
dnf install autoconf automake libtool
```

## Step 1: Verify Manifests
We need to ensure that `./manifests` folder is in sync with the templates in `deploy/chart/templates`.
* Make sure you have a clean workspace. `git status` should show no change(s) or untracked file.
* Make sure you pull the latest from `upstream`.
* Checkout `master` branch.
* Run `make release`

This should generate no change(s) in `./manifests` folder. If there are no change(s) in `./manifests` folder then you can proceed with the new release. 
You can clean your workspace 
```bash
git checkout -- .
git clean -df
```

If you see change(s) in `./manifests` folder, then:
* STOP the release process
* Consult the team and backport the changes in `./manifests` folder into the templates.


## Step 2: Bump the Version
* Bump the version in `OLM_VERSION` file. Make a new PR with this change only.
* Wait until the PR has been merged.


## Step 3: Setup Tag
If git `push` is disabled on `upstream` repository in your fork, then clone this repository so that you can push to `master` directly.

* Pull the latest.
* Make sure you are on `master` branch.
* Make a new tag that matches the version. 
* Push tag directly to this repository.

```bash
# 0.11.0 is the bumped version.
git tag -a 0.11.0 -m "Version 0.11.0"

# origin points to operator-framework/operator-lifecycle-manager
git push origin 0.11.0
```

* Confirm that new images have been built here: <https://quay.io/repository/operator-framework/olm?tab=builds>.


## Step 3: Generate Manifests
* Make sure you have a clean workspace. `git status` should show no change(s) or untracked file.
* Make sure you pull the latest from `upstream`.
* Run `make release` on `master` branch.
* Make a new PR and ensure all tests pass for merging.

Verify the following:
* The image digest in manifest file(s) matches the new tag in `quay.io`.
* `./manifests` folder only differs in version and image.

## Step 4: Generate Changelog
Changelogs for OLM are generated using [GitHub Changelog Generator](https://github.com/github-changelog-generator/github-changelog-generator).

You need to have `gem` installed on your workstation. Execute the following command to install `github-changelog-generator`.
```bash
gem install github_changelog_generator
``` 

Afterward installing it may be worth modifying the `MAX_THREAD_NUMBER` to something lower similar to what is done here: <https://github.com/github-changelog-generator/github-changelog-generator/pull/661>. Note that the referenced PR has been merged, but the number is still too high. Although 1 is a very low value, it does seem to work more reliably. (On Fedora, the install location for the gem is `~/.gem/ruby/gems/github_changelog_generator-1.14.3/lib/github_changelog_generator/octo_fetcher.rb`.)

Make sure you have a GitHub API access token. You can generate one from [tokens](https://github.com/settings/tokens) 

* Generate the changelog:
```bash
# <start-semver> is the previous version.
# <end-semver> is the new release you have made.
github_changelog_generator -u operator-framework -p operator-lifecycle-manager --since-tag=<start-semver> \
    --token=<github-api-token> --future-release=<end-semver> --pr-label="**Other changes:**" -b CHANGELOG.md
```
* Open a new PR with the changelog.

## Step 5: Create a New GitHub Release
* Create a new GtiHub release [here](https://github.com/operator-framework/operator-lifecycle-manager/releases/new)
* Choose the new tag matching the version you created.
* You can set `Title` to the same value as the tag name.
* Add the generated `changelog` to the release description.
* Save `draft` of the release.


## Step 5: QuickStart
Edit the GitHub Release:
* Upload the files `crds.yam`, `install.sh` and `olm.yaml` as release artifacts. These files are located in `deploy/upstream/quickstart`
* Add install instruction, see an [example here](https://github.com/operator-framework/operator-lifecycle-manager/releases/tag/0.10.0#Install).


## Step 6: Publish Release
* Ensure that all links are valid and works as expected.
* Publish the release!
