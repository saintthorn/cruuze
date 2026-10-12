# syntax=docker/dockerfile:1

# The whole app as one image: the Go server with the built web app embedded.
#
#   docker build -t cruuze .
#   docker run -p 8080:8080 -e DATABASE_URL=... -e APP_URL=https://... cruuze

# --- Web app ------------------------------------------------------------------
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# --- Server -------------------------------------------------------------------
FROM golang:1.27-alpine AS server
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /src/web/dist ./internal/webui/dist
# The release this image is; shown under Settings > Instance.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X cruuze/server/internal/config.Version=${VERSION}" -tags embedweb -o /out/cruuze ./cmd/cruuze \
    # Created here because the final image has no shell to create it with.
    && mkdir -p /out/data/attachments

# --- Runtime ------------------------------------------------------------------
# No shell, no package manager, not root: just the binary and CA certificates.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /out/cruuze /cruuze
COPY --from=server --chown=nonroot:nonroot /out/data /data

# Attachments go here unless S3_BUCKET is set. Mount a volume to keep them.
ENV STORAGE_DIR=/data/attachments \
    LOG_FORMAT=json
VOLUME /data

# The server listens on $PORT if the platform sets it, else on 8080.
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD ["/cruuze", "healthcheck"]

USER nonroot
ENTRYPOINT ["/cruuze"]
