# Step 1: Build binary using official Go image
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy dependency definition files first for optimal caching
COPY go.mod go.sum ./
RUN go mod download

# Copy application source code
COPY client/ ./client/

# Compile static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o client ./client

# Step 2: Create minimal final image
FROM alpine:3.19

WORKDIR /app

# Copy compiled binary from builder stage
COPY --from=builder /app/client /app/client

ENTRYPOINT ["/app/client"]
