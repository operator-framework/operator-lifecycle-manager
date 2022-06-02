# The base image is expected to contain
# /bin/opm (with a serve subcommand) and /bin/grpc_health_probe
FROM quay.io/operator-framework/opm:latest

# Configure the entrypoint and command
ENTRYPOINT ["/bin/opm"]
CMD ["serve", "/catalog"]

# Copy declarative config root into image at /configs
COPY . /catalog

# Set label for the location of the catalog root directory
# in the image
LABEL operators.operatorframework.io.index.configs.v1=/catalog