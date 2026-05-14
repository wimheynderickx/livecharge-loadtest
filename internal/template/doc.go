// Package template handles the three template-related concerns of loadtest:
//
//  1. ContextFactory — builds per-session .ctx maps from the [context] block.
//     Static values are copied as-is. Sequence generators produce monotonically
//     increasing values across all concurrent sessions (atomic counter).
//     Random generators produce a new value per session.
//
//  2. Renderer — wraps Go text/template. Given a parsed template and a
//     Context (.ctx + .session), it produces the request body bytes.
//
//  3. Extractor — pulls values out of a transport.Response. Supports paths
//     into JSON bodies (response/...), HTTP headers (header/...), the HTTP
//     status code (status), and NATS reply headers (meta/...).
//
// Plus two small helpers:
//
//  4. Predicate evaluator — applies the [[step.predicate]] rules to a session
//     map and returns the next step name (or "" to end the session).
//
//  5. Pre-calculation detector — IsStatic reports whether a template can be
//     rendered once at startup and reused, instead of rendered on every step.
//     A template qualifies if it references no .session keys and only static
//     .ctx keys.
package template
