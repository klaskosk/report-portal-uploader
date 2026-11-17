# Container image for report-portal-uploader (rpu).
#
# The final image carries the statically linked rpu binary on top of UBI 9
# minimal, so CI can distribute it as a container instead of a raw binary:
#
#   podman run --rm -e REPORTPORTAL_TOKEN -v "$PWD:/work:ro,z" \
#     quay.io/rh-ee-klaskosk/report-portal-uploader:latest \
#     upload --config /work/config.toml
#
# Build locally with: make image

# Build stage. Any Go 1.27+ image works; GOTOOLCHAIN=auto lets Go fetch the
# exact patch release named in go.mod when the builder ships an older one
# (go-toolset:1.27 currently ships 1.27.3, and go.mod asks for 1.27.4, so the
# build downloads the toolchain from proxy.golang.org).
ARG BUILDER_IMAGE=registry.access.redhat.com/ubi9/go-toolset:1.27

FROM ${BUILDER_IMAGE} AS build

ENV GOTOOLCHAIN=auto

WORKDIR /src

# Prime the module cache in its own layer so source edits do not re-download.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static build: ubi-minimal provides no Go runtime and we do not want a libc
# dependency. TARGETOS/TARGETARCH keep this Containerfile cross-buildable.
ARG TARGETOS=linux
ARG TARGETARCH=amd64
# go-toolset runs as UID 1001, so write the binary somewhere writable.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /tmp/rpu ./cmd/rpu

# Runtime stage: UBI 9 minimal (no compilers, no Go toolchain). It is ~80 MB
# larger than ubi-micro, which is the price of the CA trust store: ubi-micro
# ships no /etc/pki/tls/certs/ca-bundle.crt at all, and rpu talks to Report
# Portal over HTTPS, so with ubi-micro every request fails with
# "x509: certificate signed by unknown authority".
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

LABEL org.opencontainers.image.title="report-portal-uploader" \
      org.opencontainers.image.description="Upload xUnit/JUnit results to Report Portal" \
      org.opencontainers.image.source="https://github.com/klaskosk/report-portal-uploader" \
      org.opencontainers.image.licenses="Apache-2.0"

COPY --from=build /tmp/rpu /usr/bin/rpu

# rpu is a one-shot batch CLI: it writes only to /tmp and to paths the caller
# mounts. UID 1001 / GID 0 matches the OpenShift arbitrary-UID model and keeps
# the default non-root, so runAsNonRoot policies accept the image; under
# rootless podman GID 0 maps to the invoking user's group, so a mounted
# directory only needs to be group-readable. Pass "podman run --user 0" when
# the mount is root-only (a 0600 config, or writing launch_url_file back).
USER 1001:0

# Relative sources[].path and --results patterns resolve from /work.
WORKDIR /work

ENTRYPOINT ["/usr/bin/rpu"]
CMD ["--help"]
