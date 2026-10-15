.PHONY: proto up down tidy order inventory notification client

proto:
	mkdir -p gen/orderpb
	protoc --proto_path=proto \
		--go_out=gen/orderpb --go_opt=paths=source_relative \
		--go-grpc_out=./gen/orderpb --go-grpc_opt=paths=source_relative \
		proto/order/v1/order.proto

tidy:
	go mod tidy

up:
	docker compose up -d

down:
	docker compose down -v

order:
	go run ./cmd/order-service

inventory:
	go run ./cmd/inventory-service

notification:
	go run ./cmd/notification-service

client:
	go run ./cmd/client
