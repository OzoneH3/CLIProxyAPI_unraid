ARG UPSTREAM_IMAGE=eceasy/cli-proxy-api:latest@sha256:b8306b3965755908e1dfcfe0fa114d8d3d4a3df2769ef5e5e3c62cfdf56ee315
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/unraid-wrapper ./cmd/unraid-wrapper

FROM ${UPSTREAM_IMAGE}
COPY --from=build /out/unraid-wrapper /usr/local/bin/unraid-wrapper
COPY LICENSE /usr/share/licenses/cliproxyapi-unraid/LICENSE
COPY licenses /usr/share/licenses/cliproxyapi-unraid/dependencies
LABEL org.opencontainers.image.source="https://github.com/OzoneH3/CLIProxyAPI_unraid" \
      org.opencontainers.image.title="CLIProxyAPI for Unraid" \
      org.opencontainers.image.licenses="MIT"
WORKDIR /data
EXPOSE 8317 8318
HEALTHCHECK --interval=30s --timeout=5s --start-period=45s --retries=3 CMD ["/usr/local/bin/unraid-wrapper", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/unraid-wrapper"]
CMD []
