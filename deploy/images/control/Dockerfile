# Control plane image. Build context: a tar of tracked files, see `make image-control`.
FROM docker.io/library/golang:1.27.3-bookworm@sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9 AS build
ENV CGO_ENABLED=0 GOWORK=off GOFLAGS=-mod=readonly
WORKDIR /src/services/control
COPY api/ /src/api/
COPY pkg/license/ /src/pkg/license/
COPY tools/release/offline/ /src/tools/release/offline/
COPY services/control/ /src/services/control/
RUN go build -trimpath -ldflags='-s -w' -o /out/control ./cmd/server

# Console and portal. `pnpm fetch` fills the store from the lockfile alone, so only a
# lockfile change repeats the download, and install scripts never run.
FROM docker.io/library/node:26.13.0-bookworm-slim@sha256:bc0a2afd3b74ce12222fc8bab99271e66257f021078f5f4a5d687a77d280c1d9 AS web
ENV CI=true
RUN npm install --global pnpm@12.16.1
WORKDIR /src/web
COPY web/pnpm-lock.yaml web/pnpm-workspace.yaml web/package.json ./
RUN pnpm fetch --ignore-scripts
COPY web/ ./
RUN pnpm install --offline --frozen-lockfile --ignore-scripts && pnpm build

FROM docker.io/library/python:3.12.15-slim-bookworm@sha256:6a0e06f5ba0b373d051aabd3fffb9b873f27f9a2a3a14bcbe37dee6f0e3bbc70
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONNOUSERSITE=1
COPY --from=build /out/control /usr/local/bin/control
COPY services/control/resources/security/password-blocklist.txt /opt/projectone/security/password-blocklist.txt
COPY runtimes/data-processing/ /opt/projectone/data-processing/
COPY --from=web /src/web/apps/portal/dist/ /opt/projectone/portal/
COPY --from=web /src/web/apps/console/dist/ /opt/projectone/admin/
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/control"]
CMD ["serve", "--config", "/run/projectone/control.yaml"]
