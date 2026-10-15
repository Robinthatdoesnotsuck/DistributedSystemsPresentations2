// Order service: exposes gRPC (sync) and publishes domain events to Kafka (async).
package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	orderv1 "github.com/example/kafka-grpc-demo/gen/orderpb/order/v1"
	"github.com/example/kafka-grpc-demo/internal/events"
	"github.com/example/kafka-grpc-demo/internal/kafkax"
)

type server struct {
	orderv1.UnimplementedOrderServiceServer
	producer *kafka.Writer
}

func (s *server) CreateOrder(ctx context.Context, req *orderv1.CreateOrderRequest) (*orderv1.CreateOrderResponse, error) {
	if req.CustomerId == "" || len(req.Items) == 0 {
		return nil, status.Error(codes.InvalidArgument, "customer_id and at least one item are required")
	}

	evt := events.OrderCreated{
		EventID:       uuid.NewString(),
		OrderID:       uuid.NewString(),
		CustomerID:    req.CustomerId,
		CustomerEmail: req.CustomerEmail,
		CreatedAt:     time.Now().UTC(),
	}
	for _, it := range req.Items {
		evt.Items = append(evt.Items, events.Item{SKU: it.Sku, Quantity: it.Quantity})
	}

	payload, err := json.Marshal(evt)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not encode event")
	}

	// Key = customer ID -> all events of one customer go to the same partition,
	// so they are processed in order.
	err = s.producer.WriteMessages(ctx, kafka.Message{
		Key:     []byte(evt.CustomerID),
		Value:   payload,
		Headers: []kafka.Header{{Key: "event-type", Value: []byte("OrderCreated")}},
	})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "could not publish event: %v", err)
	}

	log.Printf("order %s accepted, event published", evt.OrderID)
	return &orderv1.CreateOrderResponse{OrderId: evt.OrderID, Status: "PENDING"}, nil
}

func main() {
	broker := getenv("KAFKA_BROKER", "localhost:9092")

	// 3 partitions -> up to 3 instances of the same group can work in parallel.
	if err := kafkax.EnsureTopic(broker, events.TopicOrderCreated, 3); err != nil {
		log.Fatalf("ensure topic: %v", err)
	}

	producer := kafkax.NewWriter([]string{broker}, events.TopicOrderCreated)
	defer producer.Close()

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	orderv1.RegisterOrderServiceServer(grpcServer, &server{producer: producer})

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		grpcServer.GracefulStop()
	}()

	log.Println("order-service listening on :50051")
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatal(err)
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
