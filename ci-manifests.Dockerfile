# this is the base image to use for the image that contains the manifests
# currently there
FROM openshift/origin-base

ADD manifests/ /manifests
LABEL io.openshift.release.operator=true

# This image doesn't need to run as root user.
USER 1001

EXPOSE 8080

