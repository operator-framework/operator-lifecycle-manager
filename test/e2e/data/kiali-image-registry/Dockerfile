# docker build -t quay.io/olmtest/installplan_e2e-registry-image:latest .
FROM quay.io/operator-framework/operator-registry-server

LABEL OLMTest="TestInstallPlanFromBundleImage"

COPY ./bundles.db /bundles.db

EXPOSE 50051
ENTRYPOINT ["/registry-server"]
CMD ["--database", "bundles.db"]
