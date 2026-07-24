package common

import (
	"testing"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// TestHandshakeNonZero asserts Handshake carries a non-zero protocol
// version and non-empty magic cookie key/value — a zero-value Handshake
// would silently accept any subprocess as a valid plugin.
func TestHandshakeNonZero(t *testing.T) {
	t.Parallel()

	if Handshake.ProtocolVersion == 0 {
		t.Errorf("Handshake.ProtocolVersion = 0, want non-zero")
	}
	if Handshake.MagicCookieKey == "" {
		t.Errorf("Handshake.MagicCookieKey = %q, want non-empty", Handshake.MagicCookieKey)
	}
	if Handshake.MagicCookieValue == "" {
		t.Errorf("Handshake.MagicCookieValue = %q, want non-empty", Handshake.MagicCookieValue)
	}
}

// TestPluginKey asserts PluginKey returns a distinct, non-empty string for
// every category value the generated Category enum defines, including the
// CATEGORY_UNSPECIFIED fallback path.
func TestPluginKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cat  commonv1.Category
		want string
	}{
		{"unspecified", commonv1.Category_CATEGORY_UNSPECIFIED, "unspecified"},
		{"model", commonv1.Category_CATEGORY_MODEL, "model"},
		{"tool", commonv1.Category_CATEGORY_TOOL, "tool"},
		{"context", commonv1.Category_CATEGORY_CONTEXT, "context"},
		{"memory", commonv1.Category_CATEGORY_MEMORY, "memory"},
		{"frontend", commonv1.Category_CATEGORY_FRONTEND, "frontend"},
		{"widget", commonv1.Category_CATEGORY_WIDGET, "widget"},
		{"slashcommand", commonv1.Category_CATEGORY_SLASHCOMMAND, "slashcommand"},
	}

	seen := make(map[string]commonv1.Category, len(tests))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := PluginKey(tt.cat)
			if got != tt.want {
				t.Errorf("PluginKey(%v) = %q, want %q", tt.cat, got, tt.want)
			}
			if got == "" {
				t.Errorf("PluginKey(%v) returned empty string", tt.cat)
			}
		})
	}

	for _, tt := range tests {
		if other, ok := seen[PluginKey(tt.cat)]; ok && other != tt.cat {
			t.Errorf("PluginKey collision: %v and %v both produce %q", other, tt.cat, PluginKey(tt.cat))
		}
		seen[PluginKey(tt.cat)] = tt.cat
	}
}

// TestPluginKeyUnknownFallsBack asserts an out-of-range Category value
// (never emitted by the generated enum, but reachable via an explicit
// conversion) still returns a non-empty, non-panicking result.
func TestPluginKeyUnknownFallsBack(t *testing.T) {
	t.Parallel()

	const bogus commonv1.Category = 99
	got := PluginKey(bogus)
	if got == "" {
		t.Errorf("PluginKey(%v) = %q, want non-empty fallback", bogus, got)
	}
}
