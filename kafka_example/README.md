# Kafka + gRPC + Go: async communication demo

```
 client --gRPC--> order-service --publish--> [ topic: orders.created (3 partitions) ]
                                                   |                      |
                                      group: inventory-service   group: notification-service
                                      (instance A, instance B)   (instance A)
```

## Prerequisites
Go 1.22+, Docker, `protoc`, `protoc-gen-go`, `protoc-gen-go-grpc`:

    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

## Run
    make up        # start Kafka (KRaft, single node)
    make proto     # generate gRPC code into gen/orderpb
    make tidy

    # each in its own terminal
    make order
    make inventory         # instance A
    make inventory         # instance B  (same group -> partitions are split)
    make notification
    make client

## Things to try
1. Fan-out: every order shows up in BOTH inventory and notification logs (different groups).
2. Load balancing: the two inventory instances each handle a subset of partitions.
3. Ordering: orders of the same customer always hit the same partition/instance.
4. Resilience: stop `notification`, send orders, restart it -> it catches up from its committed offset.
5. Replay: change the group id (or reset offsets) to reprocess the whole history.

Inspect from the broker:

    docker exec -it kafka /opt/kafka/bin/kafka-consumer-groups.sh \
      --bootstrap-server localhost:9092 --describe --group inventory-service
