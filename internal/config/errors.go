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
