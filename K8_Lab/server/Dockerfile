# Step 1: Build binary using official Go image
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy dependency definition files first for optimal caching
COPY go.mod go.sum ./
RUN go mod download

# Copy application source code
COPY server/ ./server/

# Compile static binary (CGO disabled for scratch/alpine compatibility)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o server ./server

# Step 2: Create minimal final image
FROM alpine:3.19

WORKDIR /app

# Copy compiled binary from builder stage
COPY --from=builder /app/server /app/server

# Expose gRPC port
EXPOSE 50051

ENTRYPOINT ["/app/server"]
