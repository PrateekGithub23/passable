# API image: build static Go binaries, then copy them into a minimal "distroless" image
# (no shell, no package manager: a small attack surface, ~20 MB total).

FROM golang:1.27-alpine AS build
WORKDIR /src
# Download modules first, in their own layer: rebuilds after code changes reuse the cache.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO_ENABLED=0 -> fully static binaries; -trimpath -s -w -> smaller, reproducible builds.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ /app/
EXPOSE 8080
# Runs as the image's non-root user. /app/migrate is run by Fly's release_command before each
# deploy (see fly.toml); CMD (not ENTRYPOINT) so that command can replace it.
CMD ["/app/api"]
