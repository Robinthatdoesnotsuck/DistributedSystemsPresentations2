// Simple gRPC client that creates a few orders for different customers.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	orderv1 "github.com/example/kafka-grpc-demo/gen/orderpb/order/v1"
)

func main() {
	conn, err := grpc.NewClient("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	client := orderv1.NewOrderServiceClient(conn)

	for i := 1; i <= 9; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		res, err := client.CreateOrder(ctx, &orderv1.CreateOrderRequest{
			CustomerId:    fmt.Sprintf("customer-%d", i%4),
			CustomerEmail: fmt.Sprintf("customer%d@example.com", i%4),
			Items:         []*orderv1.Item{{Sku: "SKU-123", Quantity: int32(i)}},
		})
		cancel()
		if err != nil {
			log.Printf("order %d failed: %v", i, err)
			continue
		}
		log.Printf("order %d -> id=%s status=%s", i, res.OrderId, res.Status)
	}
}
