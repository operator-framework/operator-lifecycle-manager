FROM fedora:31
RUN yum install -y skopeo

ENTRYPOINT ["skopeo"]