# Container image for report-portal-uploader (rpu).
#
# The final image carries only the statically linked rpu binary on top of
# UBI 9 micro, so CI can distribute it as a container instead of a raw binary:
#
#   podman run --rm -e REPORTPORTAL_TOKEN -v "$PWD:/work:ro,z" \
#     quay.io/rh-ee-klaskosk/report-portal-uploader:latest \
#     upload --config /work/config.toml
#
# Build locally with: make image

# Build stage. Any Go 1.27+ image works; GOTOOLCHAIN=auto lets Go fetch the
# exact patch release named in go.mod when the builder ships an older one.
ARG BUILDER_IMAGE=registry.access.redhat.com/ubi9/go-toolset:1.27

FROM ${BUILDER_IMAGE} AS build

ENV GOTOOLCHAIN=auto

WORKDIR /src

# Prime the module cache in its own layer so source edits do not re-download.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static build: ubi-micro provides no Go runtime and we do not want a libc
# dependency. TARGETOS/TARGETARCH keep this Containerfile cross-buildable.
ARG TARGETOS=linux
ARG TARGETARCH=amd64
# go-toolset runs as UID 1001, so write the binary somewhere writable.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /tmp/rpu ./cmd/rpu

# Runtime stage: UBI 9 micro (no package manager, no compilers, minimal footprint).
FROM registry.access.redhat.com/ubi9/ubi-micro:latest

LABEL org.opencontainers.image.title="report-portal-uploader" \
      org.opencontainers.image.description="Upload xUnit/JUnit results to Report Portal" \
      org.opencontainers.image.source="https://github.com/klaskosk/report-portal-uploader" \
      org.opencontainers.image.licenses="Apache-2.0"

COPY --from=build /tmp/rpu /usr/bin/rpu

# rpu is a one-shot batch CLI: it writes only to /tmp and to paths the caller
# mounts, so it runs as UID 0 to avoid bind-mount permission surprises (under
# rootless podman, container UID 0 is the invoking user, so files created in a
# writable mount stay owned by that user). Harden with "podman run --user 1001:0"
# if the mounted files are world-readable.
USER 0

# Relative sources[].path and --results patterns resolve from /work.
WORKDIR /work

ENTRYPOINT ["/usr/bin/rpu"]
CMD ["--help"]
