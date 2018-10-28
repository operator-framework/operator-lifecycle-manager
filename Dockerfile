FROM resin/raspberry-pi-alpine-golang:1.9.7-20180828 as builder

ENV GOPATH /go
ENV PATH $GOPATH/bin:/usr/local/go/bin:$PATH

WORKDIR /go/src/github.com/operator-framework/operator-lifecycle-manager
RUN mkdir -p /go/src/github.com/operator-framework/operator-lifecycle-manager
COPY . .
RUN make build


FROM resin/raspberry-pi-alpine-golang:1.9.7-20180828

ADD manifests/ /manifests
LABEL io.openshift.release.operator=true

# Copy the binary to a standard location where it will run.
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/olm /bin/olm
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/catalog /bin/catalog
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/package-server /bin/package-server

# This image doesn't need to run as root user.
USER 1001

EXPOSE 8080
EXPOSE 5443

# Apply labels as needed. ART build automation fills in others required for
# shipping, including component NVR (name-version-release) and image name. OSBS
# applies others at build time. So most required labels need not be in the source.
#
# io.k8s.display-name is required and is displayed in certain places in the
# console (someone correct this if that's no longer the case)
#
# io.k8s.description is equivalent to "description" and should be defined per
# image; otherwise the parent image's description is inherited which is
# confusing at best when examining images.
#
LABEL io.k8s.display-name="OpenShift Operator Lifecycle Manager" \
      io.k8s.description="This is a component of OpenShift Container Platform and manages the lifecycle of operators." \
      maintainer="Odin Team <aos-odin@redhat.com>"
