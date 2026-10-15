# The Storycreep container image (PRD FR-ADMIN-01 AC4, §10.8): the storycreep
# binary, with the web interface embedded in it, on a distroless base that
# has no shell, no package manager, and no build toolchain, running as a
# non-root user. Build it from the repository's root:
#
#   docker build -f deploy/Dockerfile -t storycreep .
#
# The build stage runs on the build machine's own platform and cross-compiles
# the binary for the image's, so building for both published platforms needs
# no emulation:
#
#   docker buildx build --platform linux/amd64,linux/arm64 -f deploy/Dockerfile .
#
# Dockerfile.dockerignore, next to this file, sends only the server's sources.
#
# The base images are named by tag rather than by digest, because nothing
# would update a digest: a tag picks up Go's patch releases and the base
# image's updated CA certificates and time zones at every build.

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
# The Go of this image builds the binary: a go.mod that needs a newer one
# fails the build, rather than downloading another toolchain.
ENV GOTOOLCHAIN=local CGO_ENABLED=0
WORKDIR /src/server
COPY server/ ./
ARG TARGETOS
ARG TARGETARCH
# What /version reports. The build has no Git checkout to read the revision
# from, so CI passes it (package version).
ARG STORYCREEP_VERSION=
ARG STORYCREEP_COMMIT=
ARG STORYCREEP_COMMIT_TIME=
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    v=github.com/CyberCute-Studio/storycreep/server/internal/version && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false \
      -ldflags="-s -w -X $v.release=$STORYCREEP_VERSION -X $v.commit=$STORYCREEP_COMMIT -X $v.commitTime=$STORYCREEP_COMMIT_TIME" \
      -o /out/usr/local/bin/storycreep ./cmd/storycreep
# The asset directory, where docker-compose.yml mounts a named volume. Docker
# fills a new volume from the image's directory, its owner included, so the
# server's user can write to the volume.
RUN mkdir -p /out/var/lib/storycreep/assets

FROM gcr.io/distroless/static-debian13:nonroot
ARG STORYCREEP_VERSION=
ARG STORYCREEP_COMMIT=
LABEL org.opencontainers.image.title="Storycreep" \
      org.opencontainers.image.description="A self-hostable tool for designing branching game narratives" \
      org.opencontainers.image.source="https://github.com/CyberCute-Studio/storycreep" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.version="$STORYCREEP_VERSION" \
      org.opencontainers.image.revision="$STORYCREEP_COMMIT"
COPY --from=build /out/usr/local/bin/storycreep /usr/local/bin/storycreep
COPY --from=build --chown=65532:65532 /out/var/lib/storycreep /var/lib/storycreep
# The base image's nonroot user, by number, so that a runtime that refuses to
# run as root, as Kubernetes does with runAsNonRoot, can tell that it is not
# root without reading the image's /etc/passwd.
USER 65532:65532
# STORYCREEP_HTTP_ADDR's default. The admin port, which serves /metrics,
# listens on the container's loopback interface unless STORYCREEP_ADMIN_ADDR
# says otherwise.
EXPOSE 8080
# The server reports ready once the database step of startup is done, which
# applies the pending migrations, so it gets two minutes for that.
HEALTHCHECK --interval=30s --timeout=10s --start-period=2m --start-interval=2s \
  CMD ["/usr/local/bin/storycreep", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/storycreep"]
CMD ["serve"]
