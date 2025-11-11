# OpenShift Test CRDs

## Purpose

These CRD YAML files are used **only for unit testing** the OpenShift controller (`pkg/controller/operators/openshift/`).

## Why These Files Exist

The OpenShift controller reports OLM status by managing `ClusterOperator` and `ClusterVersion` resources on OpenShift clusters. To test this controller in envtest environments (like CI or local development), we need the CRD definitions.

**Problem**: The `github.com/openshift/api` package (v0.0.0-20251111193948+) no longer ships individual CRD YAML files in vendor. They were removed in favor of a consolidated metadata-only manifest (`zz_generated.featuregated-crd-manifests.yaml`).

**Solution**: Generate minimal CRDs with the schema our tests need. These CRDs are NOT used in production - OpenShift clusters have the actual CRDs installed by the platform.

## How They're Generated

Run: `make openshift-test-crds` or `make gen-all`

This executes `scripts/generate_openshift_crds.sh` which creates:
- `clusteroperators.config.openshift.io.yaml` - Minimal ClusterOperator CRD
- `clusterversions.config.openshift.io.yaml` - Minimal ClusterVersion CRD

## How They're Used

In `suite_test.go`:

```go
testEnv = &envtest.Environment{
    CRDs: []*apiextensionsv1.CustomResourceDefinition{
        crds.ClusterServiceVersion(),
    },
    CRDDirectoryPaths: []string{
        filepath.Join("testdata", "crds"),  // <- Loads these files
    },
}
```

## DO NOT Edit Manually

These files are generated. Changes should be made in `scripts/generate_openshift_crds.sh`.

Run `make verify-manifests` to check if these files are up-to-date.
