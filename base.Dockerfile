# Dockerfile to bootstrap build and test in openshift-ci

FROM openshift/origin-release:golang-1.13

# Install test dependencies
RUN yum install -y skopeo && \
    export OS=$(go env GOOS) && \
    export ARCH=$(go env GOARCH) && \
    curl -L "https://go.kubebuilder.io/dl/2.3.1/${OS}/${ARCH}" | tar -xz -C /tmp/ && \
    mv /tmp/kubebuilder_2.3.1_${OS}_${ARCH}/ /usr/local/kubebuilder && \
    export PATH=$PATH:/usr/local/kubebuilder/bin && \
    echo "Kubebuilder installation complete!"
