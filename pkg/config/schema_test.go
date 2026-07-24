package config_test

import (
	"errors"
	"testing"

	"github.com/pluggableharness/agent/pkg/config"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
)

func TestSchema_Valid(t *testing.T) {
	t.Parallel()

	region, err := config.Attribute("region", configv1.AttrType_ATTR_TYPE_STRING, config.WithRequired())
	if err != nil {
		t.Fatalf("Attribute(region) = _, %v, want nil error", err)
	}
	apiKey, err := config.Attribute("api_key", configv1.AttrType_ATTR_TYPE_STRING, config.WithSensitive())
	if err != nil {
		t.Fatalf("Attribute(api_key) = _, %v, want nil error", err)
	}

	got, err := config.Schema(region, apiKey)
	if err != nil {
		t.Fatalf("Schema(region, api_key) = _, %v, want nil error", err)
	}
	if len(got.GetAttributes()) != 2 {
		t.Fatalf("Schema(...).GetAttributes() has %d entries, want 2", len(got.GetAttributes()))
	}
	if got.GetAttributes()[0].GetName() != "region" || got.GetAttributes()[1].GetName() != "api_key" {
		t.Errorf("Schema(...).GetAttributes() = %v, want [region api_key] order preserved", got.GetAttributes())
	}
}

func TestSchema_Empty(t *testing.T) {
	t.Parallel()

	got, err := config.Schema()
	if err != nil {
		t.Fatalf("Schema() = _, %v, want nil error", err)
	}
	if len(got.GetAttributes()) != 0 {
		t.Errorf("Schema().GetAttributes() has %d entries, want 0", len(got.GetAttributes()))
	}
}

// TestSchema_CatchesHandBuiltAttribute proves Schema re-validates its whole
// tree rather than trusting that every *configv1.ConfigAttribute it
// receives came from Attribute — see doc.go and validate.go's
// validateAttribute doc comment.
func TestSchema_CatchesHandBuiltAttribute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		attr *configv1.ConfigAttribute
		want error
	}{
		{
			name: "unspecified type",
			attr: &configv1.ConfigAttribute{Name: "attr"},
			want: config.ErrUnspecifiedType,
		},
		{
			name: "object missing object_attributes",
			attr: &configv1.ConfigAttribute{Name: "attr", Type: configv1.AttrType_ATTR_TYPE_OBJECT},
			want: config.ErrObjectAttributesMismatch,
		},
		{
			name: "sensitive with default",
			attr: &configv1.ConfigAttribute{
				Name:        "attr",
				Type:        configv1.AttrType_ATTR_TYPE_STRING,
				Sensitive:   true,
				DefaultJson: strPtr(`"literal"`),
			},
			want: config.ErrSensitiveDefault,
		},
		{
			name: "default shape mismatch",
			attr: &configv1.ConfigAttribute{
				Name:        "attr",
				Type:        configv1.AttrType_ATTR_TYPE_NUMBER,
				DefaultJson: strPtr(`"not-a-number"`),
			},
			want: config.ErrDefaultTypeMismatch,
		},
		{
			name: "invalid attribute nested inside a valid object",
			attr: &configv1.ConfigAttribute{
				Name: "attr",
				Type: configv1.AttrType_ATTR_TYPE_OBJECT,
				ObjectAttributes: []*configv1.ConfigAttribute{
					{Name: "child"}, // ATTR_TYPE_UNSPECIFIED, hand-built, never went through Attribute
				},
			},
			want: config.ErrUnspecifiedType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Schema(tt.attr)
			if !errors.Is(err, tt.want) {
				t.Errorf("Schema(%+v) error = %v, want errors.Is %v", tt.attr, err, tt.want)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
