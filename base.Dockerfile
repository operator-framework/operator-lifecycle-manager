# Dockerfile to bootstrap build and test in openshift-ci

FROM openshift/origin-release:golang-1.13

RUN yum install -y skopeo
