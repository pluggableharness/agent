package eventbus

import "testing"

func TestIsWildcardFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		filter string
		want   bool
	}{
		{"plugin.tool.github.*", true},
		{"kernel.*", true},
		{"*", true},
		{"plugin.tool.github.file_changed", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isWildcardFilter(tt.filter); got != tt.want {
			t.Errorf("isWildcardFilter(%q) = %v, want %v", tt.filter, got, tt.want)
		}
	}
}

func TestWildcardPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		filter string
		want   string
	}{
		{"plugin.tool.github.*", "plugin.tool.github."},
		{"kernel.*", "kernel."},
		{"*", ""},
	}
	for _, tt := range tests {
		if got := wildcardPrefix(tt.filter); got != tt.want {
			t.Errorf("wildcardPrefix(%q) = %q, want %q", tt.filter, got, tt.want)
		}
	}
}

func TestMatchesFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		topic  string
		filter string
		want   bool
	}{
		{
			name:   "exact match",
			topic:  "plugin.tool.github.file_changed",
			filter: "plugin.tool.github.file_changed",
			want:   true,
		},
		{
			name:   "exact mismatch",
			topic:  "plugin.tool.github.file_changed",
			filter: "plugin.tool.github.pr_opened",
			want:   false,
		},
		{
			name:   "wildcard matches same-plugin topic",
			topic:  "plugin.tool.github.file_changed",
			filter: "plugin.tool.github.*",
			want:   true,
		},
		{
			name:   "wildcard does not match a different plugin under the same category",
			topic:  "plugin.tool.gitlab.file_changed",
			filter: "plugin.tool.github.*",
			want:   false,
		},
		{
			name:   "wildcard does not match the bare prefix itself",
			topic:  "plugin.tool.github",
			filter: "plugin.tool.github.*",
			want:   false,
		},
		{
			name:   "kernel namespace wildcard",
			topic:  "kernel.event.tool_call",
			filter: "kernel.*",
			want:   true,
		},
		{
			name:   "bare wildcard matches everything",
			topic:  "anything.at.all",
			filter: "*",
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := matchesFilter(tt.topic, tt.filter); got != tt.want {
				t.Errorf("matchesFilter(%q, %q) = %v, want %v", tt.topic, tt.filter, got, tt.want)
			}
		})
	}
}
