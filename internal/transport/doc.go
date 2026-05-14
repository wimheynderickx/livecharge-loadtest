// Package transport defines the abstraction the engine uses to talk to
// the network. A Transport sends Requests and returns Responses; the engine
// never knows whether the bytes are travelling over NATS, HTTP, or anything
// else.
//
// Sub-packages:
//
//   - transport/nats — NATS request/reply, fire-and-forget, header support.
//   - transport/http — HTTP/HTTPS with shared connection pool, Basic/JWT auth.
//
// The factory in this package (New) picks the right implementation based on
// the TransportConfig.Type field. New protocols (gRPC, Kafka, ...) are added
// by introducing a new sub-package and a new case in the factory.
package transport
