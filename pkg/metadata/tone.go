package metadata

import metadatav1 "github.com/pluggableharness/agent/pkg/metadata/proto/v1"

// Tone is a closed token scale for a MetadataBlock's presentation intent.
// Frontends map each token to their own vocabulary; a block never names
// a color value.
type Tone = metadatav1.Tone

const (
	// ToneUnspecified is the zero value. Never valid for a real block.
	ToneUnspecified = metadatav1.Tone_TONE_UNSPECIFIED
	// ToneNeutral is ordinary, unemphasized content.
	ToneNeutral = metadatav1.Tone_TONE_NEUTRAL
	// ToneInfo is informational emphasis without implying success or failure.
	ToneInfo = metadatav1.Tone_TONE_INFO
	// ToneSuccess is a successful or healthy condition.
	ToneSuccess = metadatav1.Tone_TONE_SUCCESS
	// ToneWarning is a cautionary condition that is not yet a failure.
	ToneWarning = metadatav1.Tone_TONE_WARNING
	// ToneDanger is a failure or high-severity condition.
	ToneDanger = metadatav1.Tone_TONE_DANGER
)

// toneByName maps the lowercase token names plugins and config use onto
// Tone values. Unknown names fall back to ToneNeutral.
var toneByName = map[string]Tone{
	"neutral": ToneNeutral,
	"info":    ToneInfo,
	"success": ToneSuccess,
	"warning": ToneWarning,
	"danger":  ToneDanger,
}

// ToneByName resolves a lowercase tone token name. ok is false for an
// unknown name; the returned Tone is then ToneNeutral so a caller that
// ignores ok still paints something sensible.
func ToneByName(name string) (Tone, bool) {
	t, ok := toneByName[name]
	if !ok {
		return ToneNeutral, false
	}
	return t, true
}
