---
status: ready-for-agent
blocked-by: ["02-go-daemon-scaffold", "04-session-model-lifecycle"]
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

## Implementation plan

### Current state (post ticket 04)

- `Daemon` has `connCount atomic.Int32`, `connAdd()`, `connDrop()` — counts connected clients
- `Daemon` has `SetSession(*session.Session)` / `Session()` — real session with deterministic ID
- `ConnectHandler` has `connAdd`/`connDrop` func fields wired from daemon methods
- `ConnectHandler` no longer has `newID`; session ID comes from `h.daemon.Session()`
- `sendSystemStatus(stream)` writes directly to the stream via `stream.Send()`
- Branch: `feature/04-05-session-eventbus` (uncommitted)

### Files

| File | Action |
|------|--------|
| `daemon/daemon/eventbus.go` | **Create** |
| `daemon/daemon/eventbus_test.go` | **Create** |
| `daemon/daemon/daemon.go` | **Modify** |
| `daemon/daemon/handler.go` | **Modify** |
| `daemon/daemon/handler_test.go` | **Modify** |
| `daemon/daemon/doc.go` | **Modify** |

### `daemon/daemon/eventbus.go`

```go
type EventBus struct {
    subscribers map[string]chan *mortisev1.ServerEvent
    mu          sync.RWMutex
    publishCh   chan *mortisev1.ServerEvent
    drops       atomic.Int64
}
```

- `NewEventBus() *EventBus` — `publishCh` buffer 128, empty subscriber map
- `Subscribe(clientID string) <-chan *ServerEvent` — buffered channel cap 64, store under Lock
- `Unsubscribe(clientID string)` — close channel, delete from map under Lock
- `Publish(event *ServerEvent)` — non-blocking send to `publishCh` (select/default)
- `Run(ctx context.Context)` — goroutine: read from `publishCh`, snapshot subscribers under RLock, non-blocking fan-out to each subscriber channel. Drop + increment counter on full buffer, log at debug. Exit on ctx cancel.
- `SubscriberCount() int` — `len(subscribers)` under RLock
- `Close()` — close `publishCh`, then close all subscriber channels, clear map

Uses `*ServerEvent` pointer type to avoid copying protobuf messages on every fan-out.

### `daemon/daemon/daemon.go` — changes

**Add field:**
```go
EventBus *EventBus
```

**Remove:**
- `connCount atomic.Int32`
- `connAdd()` / `connDrop()` methods

**Modify `Serve()`:**
- Before constructing the HTTP mux: create `d.EventBus = NewEventBus()`, start `go d.EventBus.Run(ctx)`
- `ConnectHandler` construction drops `connAdd`/`connDrop` fields

**Modify `shutdown()`:**
- After HTTP server shutdown, before listener close: call `d.EventBus.Close()`

**Modify `ConnectedClients()`:**
- Delegate to `d.EventBus.SubscriberCount()`

### `daemon/daemon/handler.go` — changes

**`ConnectHandler` struct:**
- Remove `connAdd func()`, `connDrop func()` fields
- Add `import "github.com/google/uuid"` for client ID generation

**`Connect()` flow becomes:**
```
1. clientID = uuid.NewString()
2. subCh = h.daemon.EventBus.Subscribe(clientID)
3. log "client connected" with client_id
4. Start goroutine: for ev := range subCh { stream.Send(ev) }
5. Build SystemStatus event, publish via h.daemon.EventBus.Publish(ev)
6. Enter ClientCommand receive loop (unchanged)
7. On return: h.daemon.EventBus.Unsubscribe(clientID); log "client disconnected"
```

The subscriber goroutine is started **before** the SystemStatus publish so the event is guaranteed to be received. The subscriber channel buffer (64) provides a safety net.

**`sendSystemStatus()`** changes:
- Builds the `*ServerEvent` as before but returns it instead of sending to stream
- Caller publishes it via `EventBus.Publish()`

### `daemon/daemon/handler_test.go` — changes

**`newTestDaemon`** — add EventBus creation (created by `Serve()` in production, but tests need to set it explicitly since they call `Serve()` in a goroutine). Actually, `Serve()` creates the EventBus internally, so tests don't need to change — the EventBus is created when `Serve()` is called.

**Existing tests** — `TestHandler_Connect_SendsSystemStatus` and `TestHandler_Connect_LogsIncomingCommand` continue to work unchanged. The SystemStatus still arrives via `stream.Receive()` — the transport is the same, just routed through the bus internally.

**New test: `TestHandler_Connect_MultiClientFanOut`:**
- Start daemon with EventBus
- Connect 2 clients
- Publish a `PhaseTransitionEvent` through the bus
- Both clients receive it via `stream.Receive()`

### `daemon/daemon/eventbus_test.go`

Unit tests (no socket, no handler):

| Test | What it verifies |
|------|-----------------|
| `TestSubscribe_Publish_DeliversToAll` | 3 subscribers, 1 publish, all receive |
| `TestSubscribe_AfterPublish_NoReplay` | Publish first, then subscribe — new subscriber gets nothing |
| `TestUnsubscribe_StopsDelivery` | 2 subscribers, unsubscribe 1, publish — only remaining gets it |
| `TestPublish_FullBuffer_Drops` | Fill subscriber buffer (64), publish 65th — drops > 0 |
| `TestPublish_Concurrent_NoRace` | 100 goroutines call Publish, `go test -race` |
| `TestSubscriberCount` | Count after subscribe/unsubscribe |
| `TestRun_ExitsOnContextCancel` | `Run(ctx)` exits when ctx cancelled |
| `TestClose_CleansUp` | After `Close()`, all subscriber channels are closed |

### `main.go` — no changes

`buildDaemon` doesn't need to know about the EventBus. The bus is created inside `Serve()` and lives entirely within the `daemon/daemon` package.

### Key decisions

1. **SystemStatus through bus** — yes, per ticket spec. Subscriber goroutine starts before the publish.
2. **connCount replaced** — `EventBus.SubscriberCount()` is the single source of truth. `connCount`, `connAdd`, `connDrop` removed.
3. **Dropped events** — logged at debug level. No proto field change.
4. **Client ID** — `uuid.NewString()` per connection. Independent of session ID.
5. **Pointer type** — `chan *ServerEvent` avoids copying protobuf on every fan-out.
6. **EventBus lifecycle** — created in `Serve()`, `Close()` called in `shutdown()`. The `Run(ctx)` goroutine is tied to the same `ctx` as the HTTP server.

### Blocking edges

- Blocked by: 02-go-daemon-scaffold ✅
- Blocks: 06-agent-loop-mock-provider (publishes PhaseTransitionEvent through the bus)
