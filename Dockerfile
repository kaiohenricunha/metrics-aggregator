# ── Stage 1: build ──────────────────────────────────────────────
ARG GO_VERSION=1.26.1         # <── default
FROM golang:${GO_VERSION}-alpine AS builder
WORKDIR /app
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o metrics-aggregator .

# ── Stage 2: final image ───────────────────────────────────────
FROM alpine:3.20
ARG AGG_PORT=9090
ENV AGG_PORT=$AGG_PORT
RUN addgroup -S app && adduser -S -D -H -h /nonexistent -s /sbin/nologin -G app -u 10001 app
COPY --from=builder /app/metrics-aggregator /usr/local/bin/
LABEL org.opencontainers.image.source="https://github.com/kaiohenricunha/metrics-aggregator" \
      org.opencontainers.image.description="Prometheus metrics aggregator sidecar for multi-container pods" \
      org.opencontainers.image.licenses="Apache-2.0" \
      io.artifacthub.package.readme-url="https://raw.githubusercontent.com/kaiohenricunha/metrics-aggregator/main/README.md" \
      io.artifacthub.package.license="Apache-2.0"
EXPOSE $AGG_PORT
USER app:app
ENTRYPOINT ["metrics-aggregator"]
