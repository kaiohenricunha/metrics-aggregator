# ── Stage 1: build ──────────────────────────────────────────────
ARG GO_VERSION=1.24           # <── default
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
COPY --from=builder /app/metrics-aggregator /usr/local/bin/
EXPOSE $AGG_PORT
ENTRYPOINT ["metrics-aggregator"]
