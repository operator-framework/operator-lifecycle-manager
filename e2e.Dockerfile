# ./pkg/controller/bundle/bundle_unpacker.go requires "/bin/cp"
FROM busybox
COPY olm catalog package-server wait cpb /bin/
EXPOSE 8080
EXPOSE 5443
USER 1001
CMD ["/bin/olm"]
