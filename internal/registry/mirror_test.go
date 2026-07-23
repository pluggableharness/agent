package registry

import "testing"

func TestRegistryMirror_Resolve(t *testing.T) {
	rm := MirrorTable{
		Default: "https://default.example.com",
		Mirrors: []Mirror{
			{Prefix: "github.com/agentco/", URL: "https://mirror1.example.com"},
			{Prefix: "github.com/agentco/private-", URL: "https://mirror2.example.com"},
			{Prefix: "github.com/other/", URL: "https://mirror3.example.com"},
		},
	}

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"no match falls back to default", "gitlab.com/whatever/x", "https://default.example.com"},
		{"single match", "github.com/agentco/foo", "https://mirror1.example.com"},
		{"longest prefix wins over shorter one", "github.com/agentco/private-x", "https://mirror2.example.com"},
		{"different prefix", "github.com/other/x", "https://mirror3.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := rm.Resolve(tt.source); got != tt.want {
				t.Fatalf("Resolve(%q) = %q, want %q", tt.source, got, tt.want)
			}
		})
	}
}

func TestRegistryMirror_Resolve_noMirrors(t *testing.T) {
	t.Parallel()
	rm := MirrorTable{Default: "https://default.example.com"}
	if got := rm.Resolve("github.com/anyone/anything"); got != "https://default.example.com" {
		t.Fatalf("Resolve with no mirrors = %q, want default", got)
	}
}
