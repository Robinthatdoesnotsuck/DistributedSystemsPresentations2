# Step 1: Build binary using official Go image
FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY client/ ./client/

# Build static binary into a distinct filename (/app/grpc-client)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/grpc-client ./client

# Step 2: Create minimal final image
FROM alpine:3.19

WORKDIR /app

COPY --from=builder /app/grpc-client /app/grpc-client

ENTRYPOINT ["/app/grpc-client"]
