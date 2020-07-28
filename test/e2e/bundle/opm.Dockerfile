FROM quay.io/operator-framework/upstream-opm-builder AS builder

FROM fedora:31
COPY --from=builder /bin/opm /bin/opm
RUN yum install -y podman

