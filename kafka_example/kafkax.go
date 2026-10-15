// Package kafkax wraps segmentio/kafka-go with small helpers for the demo.
package kafkax

import (
	"context"
	"errors"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/segmentio/kafka-go"
)

// EnsureTopic creates the topic if it does not exist (idempotent).
// Partitions define the max parallelism of a consumer group.
func EnsureTopic(broker, topic string, partitions int) error {
	conn, err := kafka.Dial("tcp", broker)
	if err != nil {
		return err
	}
	defer conn.Close()

	controller, err := conn.Controller()
	if err != nil {
		return err
	}
	ctrl, err := kafka.Dial("tcp", net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port)))
	if err != nil {
		return err
	}
	defer ctrl.Close()

	return ctrl.CreateTopics(kafka.TopicConfig{
		Topic:             topic,
		NumPartitions:     partitions,
		ReplicationFactor: 1, // use 3 in production
	})
}

// NewWriter builds a producer. Messages with the same key always land on the
// same partition, which preserves ordering per key.
func NewWriter(brokers []string, topic string) *kafka.Writer {
	return &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Topic:        topic,
		Balancer:     &kafka.Hash{},    // partition = hash(key)
		RequiredAcks: kafka.RequireAll, // wait for all in-sync replicas
		BatchTimeout: 10 * time.Millisecond,
	}
}

// Handler processes a single message. Return an error to signal failure.
type Handler func(ctx context.Context, msg kafka.Message) error

// Consume joins a consumer group ("subscription") and processes messages until
// ctx is cancelled. Offsets are committed only AFTER the handler succeeds,
// giving at-least-once delivery -> handlers must be idempotent.
func Consume(ctx context.Context, brokers []string, topic, groupID string, handle Handler) error {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     brokers,
		Topic:       topic,
		GroupID:     groupID,
		StartOffset: kafka.FirstOffset, // new groups start from the beginning
		MinBytes:    1,
		MaxBytes:    10e6,
	})
	defer r.Close()

	for {
		msg, err := r.FetchMessage(ctx) // does not auto-commit
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}

		if err := processWithRetry(ctx, handle, msg, 3); err != nil {
			// In production: publish to a dead-letter topic (e.g. orders.created.dlq)
			log.Printf("giving up on partition=%d offset=%d: %v", msg.Partition, msg.Offset, err)
		}

		if err := r.CommitMessages(ctx, msg); err != nil {
			return err
		}
	}
}

func processWithRetry(ctx context.Context, h Handler, msg kafka.Message, attempts int) error {
	var err error
	for i := 1; i <= attempts; i++ {
		if err = h(ctx, msg); err == nil {
			return nil
		}
		log.Printf("attempt %d/%d failed: %v", i, attempts, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(i) * 500 * time.Millisecond):
		}
	}
	return err
}
