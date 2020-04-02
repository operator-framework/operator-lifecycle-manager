FROM scratch
COPY olm catalog package-server wait cpb /bin/
EXPOSE 8080
EXPOSE 5443
CMD ["/bin/olm"]
