# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X github.com/NandanMaheta/swastik/apps/api/internal/platform/httpx.Version=${VERSION}" \
    -o /out/swastik ./cmd/swastik

# Distroless: no shell, no package manager, runs as non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/swastik /swastik
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/swastik"]
CMD ["serve"]
