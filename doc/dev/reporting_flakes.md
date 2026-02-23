# Reporting flakes

If you are struggling to get your PR through because unrelated e2e or unit tests are randomly failing, it's likely
you are being plagued by a flaky test 😱, a test that wasn't constructed as carefully as it should have been and is
failing even when it should be succeeding. When this happens, check our [issues](https://github.com/operator-framework/operator-lifecycle-manager/issues) 
to see if it has been filed before. Search also in the `closed issues`. If you find one, re-open it if necessary. 
Otherwise, [file](https://github.com/operator-framework/operator-lifecycle-manager/issues/new) a flaky test issue.

Once you have an issue link, you can disable the flaky test by adding the `[FLAKE]` tag to the test name and linking the issue in the code.

Example:

```
    // issue: https://github.com/operator-framework/operator-lifecycle-manager/issues/2635 
    It("[FLAKE] updates multiple intermediates", func() {
...
```

You may be asked by the reviewer to supply evidence that the test is indeed flaky and not an unfortunate side effect of
your contribution. We'll endeavor to make this an easy as process as possible to merge your contribution in as quickly
and safely as possible.
