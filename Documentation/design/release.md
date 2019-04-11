# Changelog Generation

Changelogs for OLM are generated using [GitHub Changelog Generator](https://github.com/github-changelog-generator/github-changelog-generator).

```bash
github_changelog_generator -u operator-framework -p operator-lifecycle-manager --since-tag=<start-semver> \
    --token=<github-api-token> --future-release=<end-semver> --pr-label="**Other changes:**"
```