# Step 1: Build binary using official Go image
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy dependency definition files first for optimal caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source directory
COPY server/ ./server/

# Build static binary into a distinct filename (/app/grpc-server)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/grpc-server ./server

# Step 2: Create minimal final image
FROM alpine:3.19

WORKDIR /app

# Copy the compiled executable file specifically
COPY --from=builder /app/grpc-server /app/grpc-server

# Expose gRPC port
EXPOSE 50051

# Run the binary file
ENTRYPOINT ["/app/grpc-server"]
