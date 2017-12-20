FROM golang:1.10beta1 as builder
WORKDIR /go/src/github.com/coreos-inc/alm

# SSH key to fetch operator-client dependency. should be base64 encoded
# "--build-arg sshkey=`cat ~/.ssh/robot_rsa | base64 -w0`"
ARG sshkey
RUN mkdir -p ~/.ssh
RUN apt-get install make git openssh-client gcc g++

COPY glide.yaml glide.lock Makefile ./

RUN make glide \
    && echo $sshkey | base64 -d > ~/.ssh/id_rsa  \
    && chmod 400 ~/.ssh/id_rsa \
    && ssh-keyscan -t rsa github.com >> ~/.ssh/known_hosts \
    && make vendor
