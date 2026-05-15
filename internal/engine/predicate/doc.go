// Package predicate compiles and evaluates expr-lang expressions used by
// [[step.predicate]] entries with op="expr".
//
// Compilation happens once at scenario start (Compile). Evaluation runs
// per request (Eval) against a context assembled from the response and
// the session/ctx maps. Missing variables resolve to the zero value of
// the inferred type so authors can write permissive expressions without
// littering them with existence checks.
package predicate
