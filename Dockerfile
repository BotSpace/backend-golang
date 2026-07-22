FROM golang:1.26.5-alpine AS builder

WORKDIR /src

# leverage layer cache for deps
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# Build version injected into buildinfo.Version (pass --build-arg VERSION=...).
ARG VERSION=dev

# CGO disabled for static binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X github.com/botspace/backend/internal/shared/buildinfo.Version=${VERSION}" \
    -o /out/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X github.com/botspace/backend/internal/shared/buildinfo.Version=${VERSION}" \
    -o /out/worker ./cmd/worker
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X github.com/botspace/backend/internal/shared/buildinfo.Version=${VERSION}" \
    -o /out/runtime-migrate ./cmd/runtime-migrate

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata wget && \
    addgroup -S app && adduser -S app -G app

WORKDIR /app

COPY --from=builder /out/api /app/api
COPY --from=builder /out/worker /app/worker
COPY --from=builder /out/runtime-migrate /app/runtime-migrate
COPY --chown=app:app configs ./configs
COPY --chown=app:app migrations ./migrations

# Writable dir for RS256 keys. In dev they auto-generate here; in production
# mount your own freshly-generated keys (read-only) over this path.
RUN mkdir -p /app/keys && chown app:app /app/keys

USER app

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8080/healthz || exit 1

ENTRYPOINT ["/app/api"]
