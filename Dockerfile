# ── Stage 1: build ──────────────────────────────────────────────
ARG GO_VERSION=1.26.1         # <── default
# NOTE: GO_VERSION arg is informational only — the digest pin is authoritative.
FROM golang:${GO_VERSION}-alpine@sha256:2389ebfa5b7f43eeafbd6be0c3700cc46690ef842ad962f6c5bd6be49ed82039 AS builder
WORKDIR /app
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -ldflags="-X main.version=${VERSION} -X main.commitSHA=${COMMIT}" -o metrics-aggregator .

# ── Stage 2: final image ───────────────────────────────────────
FROM alpine:3.20@sha256:a4f4213abb84c497377b8544c81b3564f313746700372ec4fe84653e4fb03805
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
