// Package mockserver implements a minimal test double for OCS components.
//
// The server listens on NATS or HTTP, optionally extracts fields from the
// incoming JSON body, and replies with a templated OK or FAIL response
// chosen at random according to a per-endpoint fail_rate.
//
// What it deliberately does NOT do:
//
//   - Validate or authenticate requests.
//   - Persist state.
//   - Implement any OCS protocol semantics.
//
// Its only job is to reply fast enough that loadtest can stress the load
// generator itself without needing a real OCS system.
package mockserver
