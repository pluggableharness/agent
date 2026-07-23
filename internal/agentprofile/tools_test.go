package agentprofile

import (
	"errors"
	"maps"
	"testing"
)

func TestResolveTools(t *testing.T) {
	available := map[string][]string{
		"filesystem": {"read_file", "write_file"},
		"search":     {"web_search", "code_search"},
	}

	tests := []struct {
		name     string
		scoping  []string
		wantKeys map[string]bool
		wantErr  error
	}{
		{
			name:     "nil scoping resolves to empty set",
			scoping:  nil,
			wantKeys: map[string]bool{},
		},
		{
			name:     "empty scoping resolves to empty set",
			scoping:  []string{},
			wantKeys: map[string]bool{},
		},
		{
			name:    "wildcard expands to every advertised tool",
			scoping: []string{"search.*"},
			wantKeys: map[string]bool{
				"search.web_search":  true,
				"search.code_search": true,
			},
		},
		{
			name:    "concrete tool name resolves directly",
			scoping: []string{"filesystem.read_file"},
			wantKeys: map[string]bool{
				"filesystem.read_file": true,
			},
		},
		{
			name:    "mixed concrete and wildcard entries",
			scoping: []string{"filesystem.read_file", "search.*"},
			wantKeys: map[string]bool{
				"filesystem.read_file": true,
				"search.web_search":    true,
				"search.code_search":   true,
			},
		},
		{
			name:     "wildcard for an unloaded provider contributes nothing, no error",
			scoping:  []string{"unloaded.*"},
			wantKeys: map[string]bool{},
		},
		{
			name:     "concrete entry for an unloaded provider contributes nothing, no error",
			scoping:  []string{"unloaded.some_tool"},
			wantKeys: map[string]bool{},
		},
		{
			name:    "concrete tool name not advertised by a loaded provider is an error",
			scoping: []string{"filesystem.delete_everything"},
			wantErr: ErrUnknownTool,
		},
		{
			name:    "entry with no dot is malformed",
			scoping: []string{"filesystem"},
			wantErr: ErrMalformedToolScope,
		},
		{
			name:    "entry with empty provider is malformed",
			scoping: []string{".read_file"},
			wantErr: ErrMalformedToolScope,
		},
		{
			name:    "entry with empty tool name is malformed",
			scoping: []string{"filesystem."},
			wantErr: ErrMalformedToolScope,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ResolveTools(tt.scoping, available)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ResolveTools(%v) error = %v, want wrapping %v", tt.scoping, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveTools(%v): unexpected error: %v", tt.scoping, err)
			}
			if !maps.Equal(got, tt.wantKeys) {
				t.Errorf("ResolveTools(%v) = %v, want %v", tt.scoping, got, tt.wantKeys)
			}
		})
	}
}

func TestResolveTools_availableIsNotMutated(t *testing.T) {
	t.Parallel()

	available := map[string][]string{"search": {"web_search"}}
	if _, err := ResolveTools([]string{"search.*"}, available); err != nil {
		t.Fatalf("ResolveTools: unexpected error: %v", err)
	}
	if len(available["search"]) != 1 || available["search"][0] != "web_search" {
		t.Errorf("available mutated by ResolveTools: %v", available)
	}
}
