# Container image for report-portal-uploader (rpu).
#
# The final image carries only the statically linked rpu binary, plus the CA
# trust store it needs for HTTPS, on top of UBI 9 micro:
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

# Static build: ubi-micro provides no Go runtime and we do not want a libc
# dependency. TARGETOS/TARGETARCH keep this Containerfile cross-buildable.
ARG TARGETOS=linux
ARG TARGETARCH=amd64
# go-toolset runs as UID 1001, so write the binary somewhere writable.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /tmp/rpu ./cmd/rpu

# Trust stage. ubi-micro ships no CA trust store at all, so every HTTPS call to
# Report Portal would fail with "x509: certificate signed by unknown
# authority". ubi-minimal carries the bundle; borrow just the two paths Go and
# curl look at instead of shipping the whole (much larger) base.
#
# To trust a private CA, point SSL_CERT_FILE at a bundle containing it: Go
# still scans /etc/pki/tls/certs, so the public roots keep working. Mounting a
# combined public+private bundle over /etc/pki/tls/certs/ca-bundle.crt also
# works, but replaces the public roots instead of adding to them.
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest AS trust

# Runtime stage: UBI 9 micro (no package manager, no compilers, minimal footprint).
FROM registry.access.redhat.com/ubi9/ubi-micro:latest

LABEL org.opencontainers.image.title="report-portal-uploader" \
      org.opencontainers.image.description="Upload xUnit/JUnit results to Report Portal" \
      org.opencontainers.image.source="https://github.com/klaskosk/report-portal-uploader" \
      org.opencontainers.image.licenses="Apache-2.0"

COPY --from=build /tmp/rpu /usr/bin/rpu

# Both paths crypto/x509 looks at, copied as real files so neither depends on
# the other being present or on a symlink resolving.
COPY --from=trust /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem \
                  /etc/pki/tls/certs/ca-bundle.crt
COPY --from=trust /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem \
                  /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem

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
