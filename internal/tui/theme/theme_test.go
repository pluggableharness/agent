package theme_test

import (
	"image/color"
	"math"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/pluggableharness/agent/internal/tui/theme"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

func TestTextStyleMapsEveryEnumValue(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	// Every enum value must resolve to a style that renders its input. The
	// protocol requires a frontend with no visual distinction for a style to
	// still render the underlying text rather than dropping it.
	for value, name := range renderv1.TextStyle_name {
		style := renderv1.TextStyle(value)
		got := th.TextStyle(&style).Render("payload")

		if got == "" {
			t.Errorf("TextStyle(%s) rendered empty, dropping content", name)
		}
	}
}

func TestTextStyleUnsetIsDistinctFromNormal(t *testing.T) {
	t.Parallel()

	th := theme.Dark()
	th.Normal = th.Normal.Bold(true)

	normal := renderv1.TextStyle_TEXT_STYLE_NORMAL

	unset := th.TextStyle(nil).Render("x")
	explicit := th.TextStyle(&normal).Render("x")

	// Unset means "frontend's own default"; an explicit NORMAL is a producer
	// deliberately asking for plain styling. They are separate states and the
	// theme must keep them separately overridable.
	if unset == explicit {
		t.Fatalf("unset style and explicit NORMAL resolved identically after overriding Normal")
	}
}

func TestTextStyleUnknownValueFallsBackToDefault(t *testing.T) {
	t.Parallel()

	th := theme.Dark()
	future := renderv1.TextStyle(9999)

	got := th.TextStyle(&future).Render("from the future")
	want := th.Default.Render("from the future")

	if got != want {
		t.Fatalf("unknown TextStyle did not fall back to Default\ngot:  %q\nwant: %q", got, want)
	}
}

func TestByName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       string
		wantName string
		wantOK   bool
	}{
		{name: "dark", in: "dark", wantName: "dark", wantOK: true},
		{name: "light", in: "light", wantName: "light", wantOK: true},
		{name: "empty defaults to dark", in: "", wantName: "dark", wantOK: true},
		{name: "unknown falls back", in: "solarized", wantName: "dark", wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := theme.ByName(tc.in)
			if ok != tc.wantOK {
				t.Errorf("ByName(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			}

			if got.Name != tc.wantName {
				t.Errorf("ByName(%q) name = %q, want %q", tc.in, got.Name, tc.wantName)
			}
		})
	}
}

func TestBuiltinThemesDifferAndAreNamed(t *testing.T) {
	t.Parallel()

	dark, light := theme.Dark(), theme.Light()

	if dark.Name != "dark" || light.Name != "light" {
		t.Fatalf("themes misnamed: %q / %q", dark.Name, light.Name)
	}

	if dark.Default.Render("x") == light.Default.Render("x") {
		t.Fatal("dark and light rendered identically; palettes are not being applied")
	}
}

// Content styles must not set a background.
//
// Lip Gloss terminates every styled run with a full SGR reset, which clears any
// background a container had set. A text style carrying its own background
// therefore paints a band that stops where the text stops, leaving the rest of
// the row on the terminal's background — a visible patchwork across every pane.
// Surfaces are painted by the container that owns them; text contributes color
// only.
func TestContentStylesSetNoBackground(t *testing.T) {
	t.Parallel()

	// An unset background is a zero color value, not nil, so the comparison is
	// against what a fresh style reports rather than against nil.
	unset := lipgloss.NewStyle().GetBackground()

	for _, th := range []theme.Theme{theme.Dark(), theme.Light()} {
		styles := map[string]lipgloss.Style{
			"Default":     th.Default,
			"Normal":      th.Normal,
			"Bold":        th.Bold,
			"Italic":      th.Italic,
			"Code":        th.Code,
			"Dim":         th.Dim,
			"Error":       th.Error,
			"Warning":     th.Warning,
			"Success":     th.Success,
			"CodeBlock":   th.CodeBlock,
			"Border":      th.Border,
			"RegionTitle": th.RegionTitle,
			"Action":      th.Action,
			"DiffAdd":     th.DiffAdd,
			"DiffRemove":  th.DiffRemove,
			"DiffHeader":  th.DiffHeader,
			"TableHeader": th.TableHeader,
			"Link":        th.Link,
			"SubSession":  th.SubSession,
		}

		for name, s := range styles {
			if bg := s.GetBackground(); bg != unset {
				t.Errorf("%s theme: style %s sets a background (%v); it would band across its row", th.Name, name, bg)
			}
		}
	}
}

// The one deliberate exception: a focused action is a filled control, a single
// self-contained run whose reset lands at its own end.
func TestFocusedActionIsDeliberatelyFilled(t *testing.T) {
	t.Parallel()

	if theme.Dark().ActionFocused.GetBackground() == lipgloss.NewStyle().GetBackground() {
		t.Fatal("ActionFocused lost its fill; a focused button needs to be visibly inverted")
	}
}

func TestToneRoundTripsByName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"neutral", "primary", "accent", "success", "warning", "danger", "info"} {
		tone, ok := theme.ToneByName(name)
		if !ok {
			t.Errorf("ToneByName(%q) not found", name)

			continue
		}

		if got := tone.String(); got != name {
			t.Errorf("tone %q round-tripped to %q", name, got)
		}
	}
}

func TestUnknownToneFallsBack(t *testing.T) {
	t.Parallel()

	tone, ok := theme.ToneByName("chartreuse")
	if ok {
		t.Fatal("ToneByName accepted an unknown name")
	}

	if tone != theme.ToneNeutral {
		t.Fatalf("unknown tone = %v, want ToneNeutral", tone)
	}

	if got := theme.Tone(99).String(); got != "neutral" {
		t.Fatalf("out-of-range tone name = %q, want neutral", got)
	}
}

// Every tone must resolve to a color, including an out-of-range value.
func TestToneResolvesToDistinctColors(t *testing.T) {
	t.Parallel()

	th := theme.Dark()
	// Keyed on the channels themselves rather than a packed integer. RGBA
	// returns 16-bit channels, so packing them as r<<16|g<<8|b overlaps green
	// into red's bits and blue into green's — two distinct tones could collide
	// on one key and this test would report a duplicate that does not exist,
	// or miss one that does.
	seen := map[[3]uint32]string{}

	tones := map[theme.Tone]string{
		theme.TonePrimary: "primary",
		theme.ToneAccent:  "accent",
		theme.ToneSuccess: "success",
		theme.ToneWarning: "warning",
		theme.ToneDanger:  "danger",
		theme.ToneInfo:    "info",
	}

	for tone, name := range tones {
		r, g, b, _ := th.Tone(tone).RGBA()
		key := [3]uint32{r, g, b}

		if other, dup := seen[key]; dup {
			t.Errorf("tones %s and %s resolve to the same color", name, other)
		}

		seen[key] = name
	}

	if th.Tone(theme.Tone(99)) == nil {
		t.Fatal("out-of-range tone resolved to nil")
	}
}

func TestMixHitsItsEndpointsAndClamps(t *testing.T) {
	t.Parallel()

	black := color.RGBA{A: 0xff}
	white := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

	tests := []struct {
		name string
		at   float64
		want uint8
	}{
		{"all a", 0, 0x00},
		{"all b", 1, 0xff},
		{"below range clamps", -2, 0x00},
		{"above range clamps", 4, 0xff},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, _, _, _ := theme.Mix(black, white, tc.at).RGBA()
			if got := uint8(r >> 8); got != tc.want {
				t.Fatalf("Mix at %v = %#02x, want %#02x", tc.at, got, tc.want)
			}
		})
	}
}

// Blending happens in a perceptually uniform space, so equal steps look equal.
// A plain sRGB channel average would put the midpoint of black and white at
// 0x80, which reads far lighter than half — the whole reason gradients built
// that way band and look muddy.
func TestMixIsPerceptuallyUniform(t *testing.T) {
	t.Parallel()

	black := color.RGBA{A: 0xff}
	white := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

	mid, _, _, _ := theme.Mix(black, white, 0.5).RGBA()
	if got := uint8(mid >> 8); got >= 0x80 {
		t.Errorf("perceptual midpoint = %#02x, expected well below the sRGB average 0x80", got)
	}

	// Lightness still rises monotonically across the whole range.
	prev := -1.0

	for i := range 21 {
		r, _, _, _ := theme.Mix(black, white, float64(i)/20).RGBA()

		if got := float64(r >> 8); got < prev {
			t.Fatalf("step %d went backwards: %v after %v", i, got, prev)
		} else {
			prev = got
		}
	}
}

// A gradient across the default ramp must not dip in lightness partway, which
// is what makes an sRGB green-to-red blend look muddy in the middle.
func TestRampGradientDoesNotDipInTheMiddle(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	var minL, endL float64

	minL = 1

	for i := range 41 {
		l, _, _ := theme.ExportToOklab(theme.DefaultGaugeRamp.At(th, float64(i)/40))
		minL = math.Min(minL, l)

		if i == 40 {
			endL = l
		}
	}

	startL, _, _ := theme.ExportToOklab(theme.DefaultGaugeRamp.At(th, 0))

	// The dimmest point on the ramp should be one of its ends, not a sag
	// somewhere in between.
	if minL < math.Min(startL, endL)-0.02 {
		t.Errorf("ramp dips to lightness %.3f, below both ends (%.3f, %.3f)", minL, startL, endL)
	}
}

// A muted color must sit between its source and the background — dimmer, but
// still the same hue rather than a new one.
func TestMutedBlendsTowardBackground(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	got := th.Muted(th.C.Success)
	if got == th.C.Success {
		t.Fatal("Muted returned the original color")
	}

	if want := theme.Mix(th.C.Success, th.C.Background, theme.MutedMix); got != want {
		t.Fatalf("Muted = %v, want %v", got, want)
	}

	// Dimming a dark theme's color moves it toward the dark background, so the
	// muted variant is no brighter than the original.
	sr, sg, sb, _ := th.C.Success.RGBA()
	mr, mg, mb, _ := got.RGBA()

	if mr+mg+mb > sr+sg+sb {
		t.Error("muted variant is brighter than its source on a dark theme")
	}
}

func TestRampResolvesStopsAndInterpolates(t *testing.T) {
	t.Parallel()

	th := theme.Dark()
	ramp := theme.DefaultGaugeRamp

	// Endpoints and the midpoint land exactly on their stops.
	for _, tc := range []struct {
		at   float64
		want color.Color
		name string
	}{
		{0, th.C.Success, "start"},
		{0.5, th.C.Warning, "middle"},
		{1, th.C.Danger, "end"},
		{-1, th.C.Success, "below range clamps"},
		{2, th.C.Danger, "above range clamps"},
	} {
		if got := ramp.At(th, tc.at); got != tc.want {
			t.Errorf("%s: At(%v) = %v, want %v", tc.name, tc.at, got, tc.want)
		}
	}

	// Between stops it blends rather than snapping.
	mid := ramp.At(th, 0.25)
	if mid == th.C.Success || mid == th.C.Warning {
		t.Error("At(0.25) snapped to a stop instead of interpolating")
	}
}

// A degenerate ramp must still resolve to something visible rather than
// failing or returning nil.
func TestDegenerateRampsDegradeGracefully(t *testing.T) {
	t.Parallel()

	th := theme.Dark()

	if got := (theme.Ramp{}).At(th, 0.5); got != th.C.TextMuted {
		t.Errorf("empty ramp = %v, want the muted text token", got)
	}

	single := theme.Ramp{theme.ToneInfo}
	for _, at := range []float64{0, 0.5, 1} {
		if got := single.At(th, at); got != th.C.Info {
			t.Errorf("single-stop ramp at %v = %v, want info", at, got)
		}
	}
}

func TestThemesCarryTheDefaultGaugeRamp(t *testing.T) {
	t.Parallel()

	for _, th := range []theme.Theme{theme.Dark(), theme.Light()} {
		if len(th.GaugeRamp) != len(theme.DefaultGaugeRamp) {
			t.Errorf("%s theme has no default gauge ramp", th.Name)
		}
	}
}

// relLuminance is the WCAG relative luminance of a color.
func relLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()

	channel := func(v uint32) float64 {
		x := float64(v>>8) / 255
		if x <= 0.03928 {
			return x / 12.92
		}

		return math.Pow((x+0.055)/1.055, 2.4)
	}

	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b)
}

func contrastRatio(a, b color.Color) float64 {
	la, lb := relLuminance(a)+0.05, relLuminance(b)+0.05

	return math.Max(la, lb) / math.Min(la, lb)
}

// channelSpread is the distance between a color's strongest and weakest
// channel, in 0..255.
//
// This is the right measure for "does this grey look like a hue", and HSV
// saturation is not: near black a six-point spread is a quarter of the maximum
// channel yet completely imperceptible, so saturation flags colors that look
// perfectly neutral while missing nothing that matters. Absolute spread tracks
// what the eye actually notices.
func channelSpread(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	hi := math.Max(float64(r>>8), math.Max(float64(g>>8), float64(b>>8)))
	lo := math.Min(float64(r>>8), math.Min(float64(g>>8), float64(b>>8)))

	return hi - lo
}

// The neutral ramp must actually be neutral. An earlier ramp carried a third of
// its value in blue, and at low luminance that cast is the first thing the eye
// picks up — borders and separators stopped reading as quiet structure and
// started reading as dark blue lines.
func TestNeutralsAreNearlyGrey(t *testing.T) {
	t.Parallel()

	// #4a5570 — the periwinkle this rule exists to prevent — spreads 38.
	const maxSpread = 24.0

	for _, th := range []theme.Theme{theme.Dark(), theme.Light()} {
		neutrals := map[string]color.Color{
			"Background":        th.C.Background,
			"BackgroundPanel":   th.C.BackgroundPanel,
			"BackgroundElement": th.C.BackgroundElement,
			"BorderSubtle":      th.C.BorderSubtle,
			"Border":            th.C.Border,
			"Divider":           th.C.Divider,
			"TextSubtle":        th.C.TextSubtle,
			"TextMuted":         th.C.TextMuted,
			"Text":              th.C.Text,
		}

		for name, c := range neutrals {
			if got := channelSpread(c); got > maxSpread {
				t.Errorf("%s theme: %s spreads %.0f across channels, want at most %.0f — it will read as a hue, not a grey",
					th.Name, name, got, maxSpread)
			}
		}
	}
}

// A separator has to be visible as a line. Sharing the near-background subtle
// border token left it effectively invisible.
func TestDividerIsVisibleAgainstTheBackground(t *testing.T) {
	t.Parallel()

	for _, th := range []theme.Theme{theme.Dark(), theme.Light()} {
		got := contrastRatio(th.C.Divider, th.C.Background)
		if got < 2.0 {
			t.Errorf("%s theme: divider/background contrast %.2f, too low to read as a line", th.Name, got)
		}

		// But it must stay subordinate to the dimmest text, or it competes
		// with the content it is separating.
		if contrastRatio(th.C.Divider, th.C.Background) >= contrastRatio(th.C.TextSubtle, th.C.Background) {
			t.Errorf("%s theme: divider is as prominent as subtle text", th.Name)
		}
	}
}

// Text has to clear the usual legibility bar against the surface it sits on.
func TestTextContrastIsLegible(t *testing.T) {
	t.Parallel()

	for _, th := range []theme.Theme{theme.Dark(), theme.Light()} {
		if got := contrastRatio(th.C.Text, th.C.Background); got < 7 {
			t.Errorf("%s theme: text/background contrast %.2f, want at least 7", th.Name, got)
		}

		if got := contrastRatio(th.C.TextSubtle, th.C.Background); got < 3 {
			t.Errorf("%s theme: subtle text/background contrast %.2f, want at least 3", th.Name, got)
		}
	}
}
