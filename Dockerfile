# syntax=docker/dockerfile:1.7
FROM gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/syncctl /usr/local/bin/syncctl
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/syncctl"]