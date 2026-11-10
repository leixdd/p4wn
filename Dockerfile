# syntax=docker/dockerfile:1.7

FROM golang:1.25-alpine AS build
WORKDIR /src/go-app-api
# Build the p4wn module on its own: go.work also pulls in testtools, which the
# server does not need (and which .dockerignore keeps out of the context).
ENV GOWORK=off

COPY go-app-api/go.mod go-app-api/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY go-app-api/ ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/p4wn-server ./cmd/p4wn-server \
    && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/p4wn-server /p4wn-server
# job artifacts; owned by distroless "nonroot" (65532) so the server can write
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
EXPOSE 8081
VOLUME ["/data"]
# extra flags (e.g. -token, -max-dpi, -workers) are appended by `docker run`
ENTRYPOINT ["/p4wn-server", "-addr", ":8081", "-data-dir", "/data"]
