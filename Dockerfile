# syntax=docker/dockerfile:1.6
################## build stage ####################################
ARG GO_VERSION  # value injected by docker build
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /app
RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o metrics-aggregator .

################## final stage ####################################
FROM alpine:3.20
ARG AGG_PORT=9090
ENV AGG_PORT=$AGG_PORT
COPY --from=builder /app/metrics-aggregator /usr/local/bin/
EXPOSE $AGG_PORT
ENTRYPOINT ["metrics-aggregator"]
