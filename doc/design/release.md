# Steps to create a new release

1. Bump version in OLM_VERSION file. Make a PR and wait until it has been merged.

1. Pull change from above and make new tag with matching version. Push tag directly to this repo.

1. Confirm that new images have been built here: <https://quay.io/repository/operator-framework/olm?tab=builds>.

1. Run `make release` on master branch (easiest if done with a clean working directory). Make a PR and ensure all tests pass for merging.

## Changelog Generation

Changelogs for OLM are generated using [GitHub Changelog Generator](https://github.com/github-changelog-generator/github-changelog-generator).

If the gem command is available, one can install via `gem install github_changelog_generator`. Afterward installing it may be worth modifying the MAX_THREAD_NUMBER to something lower similar to what is done here: <https://github.com/github-changelog-generator/github-changelog-generator/pull/661>. Now the changelog can be generated:

```bash
github_changelog_generator -u operator-framework -p operator-lifecycle-manager --since-tag=<start-semver> \
    --token=<github-api-token> --future-release=<end-semver> --pr-label="**Other changes:**"
```

The resulting CHANGELOG.md file can be copied into a new release created via <https://github.com/operator-framework/operator-lifecycle-manager/releases/new>.

## QuickStart

Edit the GitHub Release and upload the files in `deploy/upstream/quickstart` as release artifacts.

Then, add instructions to the GitHub release page to install referencing those manifests.

See an [example here](https://github.com/operator-framework/operator-lifecycle-manager/releases/tag/0.10.0#Install).
