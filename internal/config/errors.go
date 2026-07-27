package config

import "errors"

// ErrDuplicateBlock is returned when a block that MUST appear at most once
// (required_providers, settings, a given provider/agent_profile name, a
// model{} block's primary) appears more than once.
var ErrDuplicateBlock = errors.New("config: duplicate block")

// ErrMissingField is returned when a required field is absent.
var ErrMissingField = errors.New("config: missing required field")

// ErrInvalidValue is returned when an attribute's evaluated value isn't
// the type or one of the enumerated strings this package expects for it.
var ErrInvalidValue = errors.New("config: invalid attribute value")

// ErrInvalidAttrType is returned when a ConfigSchema attribute declares an
// AttrType this package doesn't recognize (configuration.md §4's fixed
// 7-value subset).
var ErrInvalidAttrType = errors.New("config: invalid ConfigAttribute type")

// ErrEnvNameEmpty reports an environment{} entry with an empty name,
// which would produce a malformed entry a subprocess silently ignores.
var ErrEnvNameEmpty = errors.New("config: environment variable name is empty")

// ErrEnvNameInvalid reports an environment{} name that cannot be a POSIX
// environment variable — notably one containing "=", which would let a
// single entry smuggle in a second.
var ErrEnvNameInvalid = errors.New("config: invalid environment variable name")

// ErrEnvValueNotString reports an environment{} value that is not a
// string. A subprocess environment carries only strings, and silently
// stringifying a number would hide the config error rather than fix it.
var ErrEnvValueNotString = errors.New("config: environment variable value must be a string")

// ErrEnvValueUnusable reports an environment{} value that is null or not
// knowable at load time.
var ErrEnvValueUnusable = errors.New("config: environment variable value is null or unknown")
