// Notification service: a DIFFERENT consumer group on the same topic.
// It receives every order event too, independently of the inventory service.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/segmentio/kafka-go"

	"github.com/example/kafka-grpc-demo/internal/events"
	"github.com/example/kafka-grpc-demo/internal/kafkax"
)

func handle(_ context.Context, msg kafka.Message) error {
	var evt events.OrderCreated
	if err := json.Unmarshal(msg.Value, &evt); err != nil {
		return nil
	}
	log.Printf("[notification] sending confirmation email to %s for order %s (partition=%d offset=%d)",
		evt.CustomerEmail, evt.OrderID, msg.Partition, msg.Offset)
	return nil
}

func main() {
	broker := getenv("KAFKA_BROKER", "localhost:9092")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Println("notification-service started")
	if err := kafkax.Consume(ctx, []string{broker}, events.TopicOrderCreated, events.GroupNotification, handle); err != nil {
		log.Fatal(err)
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
