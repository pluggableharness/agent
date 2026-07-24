package plugin_test

import (
	"testing"

	"github.com/pluggableharness/agent/pkg/common"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
)

func TestIdentity_ProducerRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		id       plugin.Identity
		category commonv1.Category
	}{
		{
			name: "tool category",
			id: plugin.Identity{
				Name:    "filesystem",
				Version: "1.2.3",
				Source:  "github.com/agentco/filesystem-provider",
			},
			category: commonv1.Category_CATEGORY_TOOL,
		},
		{
			name: "model category, empty source",
			id: plugin.Identity{
				Name:    "anthropic",
				Version: "0.1.0",
			},
			category: commonv1.Category_CATEGORY_MODEL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ref := tt.id.ProducerRef(tt.category)

			if got, want := ref.GetName(), tt.id.Name; got != want {
				t.Errorf("GetName() = %q, want %q", got, want)
			}
			if got, want := ref.GetVersion(), tt.id.Version; got != want {
				t.Errorf("GetVersion() = %q, want %q", got, want)
			}
			if got, want := ref.GetSource(), tt.id.Source; got != want {
				t.Errorf("GetSource() = %q, want %q", got, want)
			}
			if got, want := ref.GetCategory(), tt.category; got != want {
				t.Errorf("GetCategory() = %v, want %v", got, want)
			}
			if got, want := ref.GetProtocolVersion(), uint32(common.ProtocolVersion); got != want {
				t.Errorf("GetProtocolVersion() = %d, want %d", got, want)
			}
		})
	}
}
