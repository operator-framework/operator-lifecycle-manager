# this is used as the `src` image in ci builds

FROM openshift/origin-release:golang-1.10
RUN yum update -y
RUN yum install -y make git

ENV GOPATH /go
ENV PATH $GOPATH/bin:/usr/local/go/bin:$PATH

WORKDIR /go/src/github.com/operator-framework/operator-lifecycle-manager
COPY . .
