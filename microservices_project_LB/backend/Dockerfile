FROM golang:1.22-alpine AS build
WORKDIR /app
COPY go.mod ./
COPY main.go ./
RUN go build -o backend main.go

FROM alpine:latest
WORKDIR /app
COPY --from=build /app/backend .
EXPOSE 8080
CMD ["./backend"]
