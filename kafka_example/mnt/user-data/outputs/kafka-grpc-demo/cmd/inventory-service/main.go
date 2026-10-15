// Inventory service: subscribes to orders.created with its own consumer group.
// Run several instances to see partitions being balanced between them.
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

// In-memory dedup for the demo. In production use a DB unique constraint
// or an "processed_events" table.
var processed = map[string]struct{}{}

func handle(_ context.Context, msg kafka.Message) error {
	var evt events.OrderCreated
	if err := json.Unmarshal(msg.Value, &evt); err != nil {
		return nil // poison message: skip (or send to DLQ)
	}

	if _, seen := processed[evt.EventID]; seen {
		log.Printf("[inventory] duplicate event %s ignored", evt.EventID)
		return nil
	}

	for _, it := range evt.Items {
		log.Printf("[inventory] reserving %d x %s for order %s (partition=%d offset=%d)",
			it.Quantity, it.SKU, evt.OrderID, msg.Partition, msg.Offset)
	}
	processed[evt.EventID] = struct{}{}
	return nil
}

func main() {
	broker := getenv("KAFKA_BROKER", "localhost:9092")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Println("inventory-service started")
	if err := kafkax.Consume(ctx, []string{broker}, events.TopicOrderCreated, events.GroupInventory, handle); err != nil {
		log.Fatal(err)
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
