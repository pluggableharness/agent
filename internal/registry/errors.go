package registry

import "errors"

// ErrInvalidValue is returned when an attribute's evaluated value isn't
// the type this package expects for it (e.g. a non-string dev_overrides
// entry).
var ErrInvalidValue = errors.New("registry: invalid attribute value")

// ErrUnsupportedLockFileVersion is returned when a lock file's
// lock_file_version is newer than this package understands.
// configuration.md §11: the kernel MUST refuse to read the rest of a lock
// file it doesn't understand, mirroring state-backend.md §9.1's schema
// migration posture.
var ErrUnsupportedLockFileVersion = errors.New("registry: unsupported lock file version")

// ErrChecksumNotRecorded is returned when a lock file has no checksum
// entry for the platform being verified.
var ErrChecksumNotRecorded = errors.New("registry: no checksum recorded for platform")

// ErrChecksumMismatch is returned when a binary's computed checksum
// doesn't match the lock file's recorded value for its platform.
var ErrChecksumMismatch = errors.New("registry: checksum mismatch")
