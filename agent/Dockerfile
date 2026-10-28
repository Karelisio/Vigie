# syntax=docker/dockerfile:1.7
# Image multi-arch (amd64 + arm64) : compilation croisée Go, sans émulation.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/vigie-agent ./cmd/vigie-agent \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12
LABEL org.opencontainers.image.title="vigie-agent" \
      org.opencontainers.image.description="Agent de supervision Vigie : métriques, conteneurs Docker, checks HTTP et alertes push" \
      org.opencontainers.image.source="https://github.com/Karelisio/Vigie" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/vigie-agent /vigie-agent
COPY --from=build /out/data /data
ENV VIGIE_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD ["/vigie-agent", "healthcheck"]
ENTRYPOINT ["/vigie-agent"]
