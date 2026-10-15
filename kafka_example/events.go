// Package events defines the event contracts shared between services.
// Treat these like a public API: evolve them in a backward-compatible way.
package events

import "time"

const (
	// TopicOrderCreated is the topic where the Order service publishes facts.
	TopicOrderCreated = "orders.created"

	// Consumer groups = "subscriptions". Each group gets its own copy of every event.
	GroupInventory    = "inventory-service"
	GroupNotification = "notification-service"
)

type Item struct {
	SKU      string `json:"sku"`
	Quantity int32  `json:"quantity"`
}

// OrderCreated is an immutable fact: "this order was created".
type OrderCreated struct {
	EventID       string    `json:"event_id"` // lets consumers deduplicate (idempotency)
	OrderID       string    `json:"order_id"`
	CustomerID    string    `json:"customer_id"`
	CustomerEmail string    `json:"customer_email"`
	Items         []Item    `json:"items"`
	CreatedAt     time.Time `json:"created_at"`
}
