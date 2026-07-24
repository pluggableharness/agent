// Package statebackend implements the kernel state backend specified in docs/specifications/state-backend.md.
// It provides sqlite-per-session persistence: an append-only event log, session metadata, cost ledger, and plan audit trail.
package statebackend
