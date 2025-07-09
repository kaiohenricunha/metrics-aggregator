# Stage 1: Build the Go app
# - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - -
FROM golang:1.22-alpine as builder
LABEL stage=builder

WORKDIR /app

# Install git
RUN apk add --no-cache git

# Copy go modules
COPY go.mod ./

# Copy source code
COPY . .

# Build the app
RUN go build -o main .

# Stage 2: Create the final image
# - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - -
FROM alpine

# Copy the built app from the builder stage
COPY --from=builder /app/main /main

# Expose port
EXPOSE 8080

# Command to run
CMD ["/main"]
