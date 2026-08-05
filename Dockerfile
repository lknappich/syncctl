# syntax=docker/dockerfile:1.7
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/syncctl /usr/local/bin/syncctl
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/syncctl"]