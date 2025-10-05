# Use the official Golang image as a builder
FROM golang:1.25.0-alpine AS builder

# Install build tools and libwebp for CGO
RUN apk --no-cache add build-base git libwebp-dev

# Set the working directory
WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download all dependencies.
# Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source code
COPY . .

# Build the Go app with CGO enabled
# CGO_ENABLED=0 is important for a static binary
# -ldflags="-w -s" strips debug information to reduce binary size
RUN go build -ldflags="-w -s" -o /app/main .

# Start a new stage from Alpine
FROM alpine:latest

# Install ca-certificates to allow the Go app to make HTTPS requests
RUN apk --no-cache add ca-certificates

# Install GitHub CLI
RUN apk --no-cache add github-cli

WORKDIR /app

# Copy the static binary from the builder stage
COPY --from=builder /app/main .
# Copy the compiled C library for webp support
COPY --from=builder /usr/lib/libwebp.so.7 /usr/lib/

# Set the entrypoint for the container
ENTRYPOINT ["/app/main"]
