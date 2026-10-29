# Image of the ops CLI, built by GoReleaser on each release (dockers_v2).
# The review app controller task runs it: ops reviewapp <cmd> --in-cluster.
# Review app commands use the AWS SDK only, so the image carries no shell or
# external binaries.
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/ops /usr/bin/ops
ENTRYPOINT ["/usr/bin/ops"]
