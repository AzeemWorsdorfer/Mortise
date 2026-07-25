---
status: ready-for-agent
blocked-by: ["02-go-daemon-scaffold"]
---

# 05 — Event Bus

**What to build:** A channel-based event bus inside the Go daemon that fans out every `ServerEvent` to all connected TUI clients. Publishers push events to a single channel; the bus fans them out to every subscriber. Clients subscribe on connect and unsubscribe on disconnect. This is the daemon's internal message backbone that all later tickets publish through.

## What to build

### EventBus struct

```go
type EventBus struct {
    subscribers map[string]chan ServerEvent
    mu          sync.RWMutex
    publishCh   chan ServerEvent
}
```

- `Subscribe(clientID string) <-chan ServerEvent` — creates a buffered channel (capacity 64) for the client, returns the read end. Client ID is derived from the connection (e.g., socket fd or UUID).
- `Unsubscribe(clientID string)` — closes the client's channel and removes from the map.
- `Publish(event ServerEvent)` — non-blocking send to every subscriber. If a subscriber's buffer is full, drop the event and increment a dropped counter (logged at debug level, surfaced in SystemStatus as a warning).
- `Run(ctx context.Context)` — goroutine that reads from `publishCh` and fans out. Exits when ctx is cancelled.

### Integration with Unix socket transport

- When a client connects over the Unix socket: generate a client ID, subscribe to the event bus, and start a goroutine that reads from the subscription channel and writes protobuf-encoded events to the socket
- When a client disconnects: unsubscribe, close the socket
- The initial `SystemStatus` event on connect is published through the event bus (not a special-case direct write)

### Thread safety

- `Subscribe`/`Unsubscribe` are safe to call concurrently from different goroutines
- `Publish` is safe to call from any goroutine
- Client goroutines reading from subscription channels handle slow consumers gracefully (don't block the publisher)

### Observability

- SystemStatus includes `connected_clients` (count of current subscribers)
- Log each connect/disconnect at info level with client ID
- Log dropped events at debug level with count

## Acceptance criteria

- [ ] Publishing an event delivers it to all connected subscribers
- [ ] A subscriber that connects after a publish does not receive past events (no replay, no buffer leakage)
- [ ] Unsubscribing a client stops delivery to that client; other subscribers are unaffected
- [ ] Publishing to a full subscriber buffer drops the event (doesn't block the publisher) and increments the drop counter
- [ ] 100 concurrent publish calls complete without race conditions (`go test -race` passes)
- [ ] `SystemStatus.connected_clients` reflects the accurate subscriber count
