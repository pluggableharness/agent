package statebackend

import "errors"

// ErrSchemaTooNew is returned when a session file's schema version is newer than this kernel understands.
var ErrSchemaTooNew = errors.New("statebackend: session file schema is newer than this kernel supports")

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("statebackend: not found")

// ErrDuplicateEventID is returned when an event with the same ID is attempted to be inserted.
var ErrDuplicateEventID = errors.New("statebackend: duplicate event id")

// ErrInvalidKind is returned when an event's kind is invalid or unspecified.
var ErrInvalidKind = errors.New("statebackend: invalid event kind")

// ErrUnrecoverable is returned when a session file is corrupted and recovery failed.
var ErrUnrecoverable = errors.New("statebackend: session file unrecoverable")

// ErrClosed is returned when an operation is attempted on a closed session store.
var ErrClosed = errors.New("statebackend: closed")
