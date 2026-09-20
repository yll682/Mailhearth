# --- Stage 1: web client ---------------------------------------------------
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# --- Stage 2: Go binary (CGO-free, static) --------------------------------
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/web/dist ./internal/web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/mailhearth ./cmd/mailhearth

# --- Stage 3: runtime -------------------------------------------------------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 10001 mailhearth
COPY --from=build /out/mailhearth /usr/local/bin/mailhearth
ENV MAILHEARTH_LISTEN=:8080 \
    MAILHEARTH_DATA_DIR=/data
VOLUME ["/data"]
EXPOSE 8080
USER mailhearth
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD wget -qO- http://127.0.0.1:8080/api/setup/status >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/mailhearth"]
