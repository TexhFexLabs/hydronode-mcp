# syntax=docker/dockerfile:1

# The build stage runs on the build host and cross-compiles, so CI never executes anything
# on the target architecture.
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS build
# No defaults: buildx sets both per platform; a default would win and put an amd64 binary into the arm64 image.
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ENV GOTOOLCHAIN=local
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
      go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/hnmcp ./cmd/hnmcp

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="hnmcp" \
      org.opencontainers.image.description="HydroNode hosted MCP server" \
      org.opencontainers.image.source="https://github.com/TexhFexLabs/hydronode-mcp" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION" \
      org.opencontainers.image.vendor="TexhFex Labs"
COPY --from=build /out/hnmcp /usr/local/bin/hnmcp
USER nonroot
# 8080: /mcp, metadata and health (behind Traefik, never published directly)
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=3 CMD ["/usr/local/bin/hnmcp", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/hnmcp"]
