# syntax=docker/dockerfile:1.7

# Build natively and cross-compile (Go under QEMU emulation segfaults).
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src/go-app-api
# Build the p4wn module on its own: go.work also pulls in testtools, which the
# server does not need (and which .dockerignore keeps out of the context).
ENV GOWORK=off

COPY go-app-api/go.mod go-app-api/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Default CJK fallback font: IPAex Gothic (IPA Font License, redistributable).
RUN apk add --no-cache font-ipaex

COPY go-app-api/ ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/p4wn-server ./cmd/p4wn-server \
    && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/p4wn-server /p4wn-server
COPY --from=build /usr/share/fonts/ipaexfont/ipaexg.ttf /usr/share/fonts/ipaexg.ttf
# Override with P4WN_CJK_FONT or -cjk-font (e.g. a mounted font).
ENV P4WN_CJK_FONT=/usr/share/fonts/ipaexg.ttf
# job artifacts; owned by distroless "nonroot" (65532) so the server can write
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
EXPOSE 8081
VOLUME ["/data"]
# extra flags (e.g. -token, -max-dpi, -workers) are appended by `docker run`
ENTRYPOINT ["/p4wn-server", "-addr", ":8081", "-data-dir", "/data"]
