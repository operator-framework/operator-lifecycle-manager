# Dockerfile to bootstrap build and test in openshift-ci

FROM openshift/origin-release:golang-1.13

ARG KUBEBUILDER_RELEASE=2.3.1
RUN yum install -y skopeo && \
    export OS=$(go env GOOS) && \
    export ARCH=$(go env GOARCH) && \
    curl -L "https://github.com/kubernetes-sigs/kubebuilder/releases/download/v${KUBEBUILDER_RELEASE}/kubebuilder_${KUBEBUILDER_RELEASE}_${OS}_${ARCH}.tar.gz" | tar -xz -C /tmp/ && \
    mv /tmp/kubebuilder_${KUBEBUILDER_RELEASE}_${OS}_${ARCH}/ /usr/local/kubebuilder && \
    export PATH=$PATH:/usr/local/kubebuilder/bin && \
    kubebuilder version && \
    echo "Kubebuilder installation complete!"
