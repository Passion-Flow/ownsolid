# Control plane image. Build context: a tar of tracked files, see `make image-control`.
FROM docker.io/library/golang:1.27.3-bookworm@sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9 AS build
ENV CGO_ENABLED=0 GOWORK=off GOFLAGS=-mod=readonly
WORKDIR /src/services/control
COPY api/ /src/api/
COPY pkg/license/ /src/pkg/license/
COPY deploy/offline/ /src/deploy/offline/
COPY tools/release/offline/ /src/tools/release/offline/
COPY services/control/ /src/services/control/
RUN go build -trimpath -ldflags='-s -w' -o /out/control ./cmd/server

FROM docker.io/library/python:3.12.15-slim-bookworm@sha256:6a0e06f5ba0b373d051aabd3fffb9b873f27f9a2a3a14bcbe37dee6f0e3bbc70
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONNOUSERSITE=1
COPY --from=build /out/control /usr/local/bin/control
COPY services/control/resources/security/password-blocklist.txt /opt/projectone/security/password-blocklist.txt
COPY runtimes/data-processing/ /opt/projectone/data-processing/
# Replaced by the web build once web/ exists (UX-001).
COPY deploy/images/control/assets/index.html /opt/projectone/portal/index.html
COPY deploy/images/control/assets/index.html /opt/projectone/admin/index.html
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/control"]
CMD ["serve", "--config", "/run/projectone/control.yaml"]
