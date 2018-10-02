# this is the base image to use for the images that don't contain manifests
# currently the catalog and package-server images
FROM openshift/origin-base

# This image doesn't need to run as root user.
USER 1001

EXPOSE 8080

