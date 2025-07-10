# ── Stage 1: build ──────────────────────────────────────────────
FROM golang:1.24-alpine AS builder
WORKDIR /app

RUN apk add --no-cache git

# copy and cache deps
COPY go.mod go.sum ./
RUN go mod download

# copy source
COPY . .

# build the single main package in the root
RUN CGO_ENABLED=0 go build -o metrics-aggregator .

# ── Stage 2: final image ───────────────────────────────────────
FROM alpine:3.20

ARG AGG_PORT=9090
ENV AGG_PORT=${AGG_PORT}

COPY --from=builder /app/metrics-aggregator /usr/local/bin/metrics-aggregator
EXPOSE ${AGG_PORT}

ENTRYPOINT ["/usr/local/bin/metrics-aggregator"]
