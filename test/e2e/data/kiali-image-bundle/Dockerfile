# bundle image containining kiali manifests for e2e test
# docker build -t quay.io/olmtest/installplan_e2e-bundle-image .
FROM registry.access.redhat.com/ubi8/ubi-minimal

LABEL OLMTest="TestInstallPlanFromBundleImage"

COPY manifests /manifests
COPY metadata /metadata
