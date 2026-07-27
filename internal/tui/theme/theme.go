package theme

import (
	"image/color"
	"math"

	"charm.land/lipgloss/v2"

	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// Palette is the raw color layer: the literal values a theme is built from,
// named for what they are rather than what they are used for.
//
// Nothing outside this package should reference a Palette field directly. It
// exists to be mapped onto Tokens, exactly as a design system keeps its raw
// ramp separate from the semantic names that reference it — the palette answers
// "what colors exist", Tokens answers "what do we use them for".
type Palette struct {
	// Neutral is the surface-to-text ramp, darkest first for a dark theme and
	// lightest first for a light one. Index 0 is the app background and index 9
	// is the highest-contrast text.
	Neutral [10]string

	Primary string
	Accent  string
	Success string
	Warning string
	Danger  string
	Info    string

	// OnAccent is the text color placed on top of a filled accent surface.
	OnAccent string
}

// Tokens is the semantic color layer: every color the shell paints with,
// named for its role.
//
// What makes the UI read as paneled is borders, titles, and spacing — not
// competing surface colors. Background is the whole application surface and is
// set once on the Bubble Tea View; BackgroundPanel and BackgroundElement exist
// for genuinely filled, self-contained controls (a badge, a focused button),
// not for broad regions.
//
// The reason is mechanical rather than aesthetic. Lip Gloss ends every styled
// run with a full SGR reset, which clears any background a container set, so a
// broadly-filled region only stays filled until the first styled run inside it
// ends. Filling large areas is therefore unreliable, and a single flat
// application surface is both correct and calmer to look at. See New for the
// rule this places on content styles.
type Tokens struct {
	Background        color.Color
	BackgroundPanel   color.Color
	BackgroundElement color.Color

	Text       color.Color
	TextMuted  color.Color
	TextSubtle color.Color
	OnAccent   color.Color

	Border       color.Color
	BorderSubtle color.Color
	BorderActive color.Color
	// Divider is for inline separators between fields, which have a different
	// job from a panel edge and therefore a different weight. A border frames a
	// region and reads as structure even when dim; a lone separator glyph sits
	// among text and needs more contrast to register at all. Sharing one token
	// between them leaves one of the two wrong.
	Divider color.Color

	Primary color.Color
	Accent  color.Color
	Success color.Color
	Warning color.Color
	Danger  color.Color
	Info    color.Color

	DiffAdded      color.Color
	DiffRemoved    color.Color
	DiffContext    color.Color
	DiffHunkHeader color.Color
}

// Tone names a color role rather than a color. It is the selector a
// configuration file uses when it wants to say "this thing is amber" without
// naming a hex value the theme should own — `color = "warning"` resolves
// through ToneByName and then through Theme.Tone, so a custom theme recolors
// everything that referenced it.
type Tone int

// The tone roles, in the order their config-facing names are declared below.
const (
	// ToneNeutral is the default, muted role.
	ToneNeutral Tone = iota
	// TonePrimary is the main accent used for focus and interaction.
	TonePrimary
	// ToneAccent is the secondary accent.
	ToneAccent
	// ToneSuccess marks a positive or completed state.
	ToneSuccess
	// ToneWarning marks something worth attention, short of an error.
	ToneWarning
	// ToneDanger marks a failure or a destructive action.
	ToneDanger
	// ToneInfo marks neutral, informational emphasis.
	ToneInfo
)

// toneNames is the config-facing spelling of each tone, in declaration order.
var toneNames = [...]string{"neutral", "primary", "accent", "success", "warning", "danger", "info"}

// String returns the tone's config-facing name.
func (t Tone) String() string {
	if int(t) < 0 || int(t) >= len(toneNames) {
		return toneNames[ToneNeutral]
	}

	return toneNames[t]
}

// ToneByName resolves a configured tone name. An unknown name resolves to
// ToneNeutral with ok false, so a caller can report the bad value rather than
// failing startup over a cosmetic setting.
func ToneByName(name string) (Tone, bool) {
	for i, n := range toneNames {
		if n == name {
			return Tone(i), true
		}
	}

	return ToneNeutral, false
}

// Tone resolves a role to this theme's color for it.
func (t Theme) Tone(tone Tone) color.Color {
	switch tone {
	case TonePrimary:
		return t.C.Primary
	case ToneAccent:
		return t.C.Accent
	case ToneSuccess:
		return t.C.Success
	case ToneWarning:
		return t.C.Warning
	case ToneDanger:
		return t.C.Danger
	case ToneInfo:
		return t.C.Info
	case ToneNeutral:
		return t.C.TextMuted
	default:
		return t.C.TextMuted
	}
}

// Mix blends two colors, with t running from 0 (all a) to 1 (all b).
//
// This is how the theme derives a shade instead of hardcoding one: a muted
// variant is the token mixed toward Background, and a gauge's intermediate hue
// is one ramp stop mixed toward the next. Both stay expressed in terms of
// tokens, so a custom theme recolors them along with everything else — which is
// the whole reason the palette layer exists.
//
// The blend happens in Oklab rather than in sRGB. Interpolating sRGB channels
// directly is the obvious implementation and it looks wrong: the path from
// green to red passes through a muddy olive, the midpoint is noticeably darker
// than either end, and a gradient built from it bands visibly because equal
// numeric steps are not equal perceptual steps. Oklab is designed so that equal
// distances look equal, which is exactly what a gradient needs.
func Mix(a, b color.Color, t float64) color.Color {
	t = math.Min(math.Max(t, 0), 1)

	al, aa, ab2 := toOklab(a)
	bl, ba, bb2 := toOklab(b)

	return fromOklab(
		al+(bl-al)*t,
		aa+(ba-aa)*t,
		ab2+(bb2-ab2)*t,
	)
}

// srgbToLinear removes the sRGB transfer function.
func srgbToLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}

	return math.Pow((c+0.055)/1.055, 2.4)
}

// linearToSrgb reapplies it.
func linearToSrgb(c float64) float64 {
	if c <= 0.0031308 {
		return c * 12.92
	}

	return 1.055*math.Pow(c, 1/2.4) - 0.055
}

// toOklab converts a color into Oklab, the perceptually uniform space this
// theme interpolates in. The matrices are Björn Ottosson's published values.
func toOklab(c color.Color) (lightness, greenRed, blueYellow float64) {
	r32, g32, b32, _ := c.RGBA()
	r := srgbToLinear(float64(r32>>8) / 255)
	g := srgbToLinear(float64(g32>>8) / 255)
	b := srgbToLinear(float64(b32>>8) / 255)

	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)

	return 0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		0.0259040371*l + 0.7827717662*m - 0.8086757660*s
}

// fromOklab is the inverse, clamped back into displayable sRGB.
func fromOklab(lightness, greenRed, blueYellow float64) color.Color {
	l := lightness + 0.3963377774*greenRed + 0.2158037573*blueYellow
	m := lightness - 0.1055613458*greenRed - 0.0638541728*blueYellow
	s := lightness - 0.0894841775*greenRed - 1.2914855480*blueYellow

	l, m, s = l*l*l, m*m*m, s*s*s

	channel := func(v float64) uint8 {
		return uint8(math.Round(math.Min(math.Max(linearToSrgb(v), 0), 1) * 255))
	}

	return color.RGBA{
		R: channel(4.0767416621*l - 3.3077115913*m + 0.2309699292*s),
		G: channel(-1.2684380046*l + 2.6097574011*m - 0.3413193965*s),
		B: channel(-0.0041960863*l - 0.7034186147*m + 1.7076147010*s),
		A: 0xff,
	}
}

// MutedMix is how far a muted variant is blended toward the background. Enough
// to read as "not active" without becoming invisible.
const MutedMix = 0.55

// Muted returns a dimmed variant of a color, blended toward this theme's
// background.
func (t Theme) Muted(c color.Color) color.Color { return Mix(c, t.C.Background, MutedMix) }

// Ramp is an ordered list of tone stops a gauge interpolates across.
//
// It is a list of *roles*, not colors, so configuration can express a ramp as
// `gauge_ramp = ["success", "warning", "danger"]` and the active theme decides
// what those look like.
type Ramp []Tone

// DefaultGaugeRamp runs green through amber to red — the universally read
// pressure ramp.
var DefaultGaugeRamp = Ramp{ToneSuccess, ToneWarning, ToneDanger}

// At resolves the ramp at position f, from 0 to 1, interpolating between the
// two stops it falls between. An empty ramp resolves to the muted text token so
// a misconfigured ramp degrades to something visible rather than to nothing.
func (r Ramp) At(t Theme, f float64) color.Color {
	switch len(r) {
	case 0:
		return t.C.TextMuted
	case 1:
		return t.Tone(r[0])
	}

	f = math.Min(math.Max(f, 0), 1)

	// Position along the ramp in stop-index space; the fractional part is how
	// far between this stop and the next.
	pos := f * float64(len(r)-1)
	i := int(pos)

	if i >= len(r)-1 {
		return t.Tone(r[len(r)-1])
	}

	return Mix(t.Tone(r[i]), t.Tone(r[i+1]), pos-float64(i))
}

// Spacing scale, in terminal cells. Every pad, gap, and gutter in the shell
// uses one of these rather than a literal, which is what keeps rhythm
// consistent across panes written at different times.
const (
	Space0 = 0
	Space1 = 1
	Space2 = 2
	Space3 = 3
	Space4 = 4
)

// Gutter is the breathing room between the screen edge and the outermost pane.
const Gutter = Space1

// Border presets. The shell picks from this set rather than calling Lip Gloss
// border constructors at the point of use, so a change of border language is
// one edit here.
var (
	BorderNone    = lipgloss.HiddenBorder()
	BorderNormal  = lipgloss.NormalBorder()
	BorderRounded = lipgloss.RoundedBorder()
	BorderThick   = lipgloss.ThickBorder()
)

// Theme is a resolved theme: its semantic tokens plus the Lip Gloss styles the
// painter uses, derived from those tokens so the two can never disagree.
type Theme struct {
	Name string
	C    Tokens
	// GaugeRamp is the ramp a pressure gauge interpolates across.
	GaugeRamp Ramp

	// Text-style tokens, one per TextStyle enum value plus the unset case.
	Default lipgloss.Style
	Normal  lipgloss.Style
	Bold    lipgloss.Style
	Italic  lipgloss.Style
	Code    lipgloss.Style
	Dim     lipgloss.Style
	Error   lipgloss.Style
	Warning lipgloss.Style
	Success lipgloss.Style

	// Chrome roles owned by the shell rather than by the protocol.
	CodeBlock     lipgloss.Style
	Border        lipgloss.Style
	BorderFocused lipgloss.Style
	RegionTitle   lipgloss.Style
	Action        lipgloss.Style
	ActionFocused lipgloss.Style
	DiffAdd       lipgloss.Style
	DiffRemove    lipgloss.Style
	DiffHeader    lipgloss.Style
	TableHeader   lipgloss.Style
	Link          lipgloss.Style
	SubSession    lipgloss.Style
}

// TextStyle resolves a TextNode's optional style pointer to a concrete style.
// A nil pointer means the producer left style unset and gets Default; an
// explicit TEXT_STYLE_NORMAL gets Normal. Any value this build does not
// recognize — including one added to the enum after this shell shipped — falls
// back to Default rather than being dropped, which is what
// docs/specifications/frontend/render-tree.md requires of every frontend.
func (t Theme) TextStyle(style *renderv1.TextStyle) lipgloss.Style {
	if style == nil {
		return t.Default
	}

	switch *style {
	case renderv1.TextStyle_TEXT_STYLE_NORMAL:
		return t.Normal
	case renderv1.TextStyle_TEXT_STYLE_BOLD:
		return t.Bold
	case renderv1.TextStyle_TEXT_STYLE_ITALIC:
		return t.Italic
	case renderv1.TextStyle_TEXT_STYLE_CODE:
		return t.Code
	case renderv1.TextStyle_TEXT_STYLE_DIM:
		return t.Dim
	case renderv1.TextStyle_TEXT_STYLE_ERROR:
		return t.Error
	case renderv1.TextStyle_TEXT_STYLE_WARNING:
		return t.Warning
	case renderv1.TextStyle_TEXT_STYLE_SUCCESS:
		return t.Success
	case renderv1.TextStyle_TEXT_STYLE_UNSPECIFIED:
		return t.Default
	default:
		return t.Default
	}
}

// DarkPalette is the raw ramp behind Dark.
var DarkPalette = Palette{
	// Near-neutral greys with only a hint of cool. An earlier ramp carried a
	// third of its value in blue — #4a5570 is periwinkle, not grey — and at low
	// luminance that cast is the first thing the eye picks up, so borders and
	// separators read as "dark blue lines" rather than as quiet structure.
	// Saturation here stays under roughly a fifth at every step.
	Neutral: [10]string{
		"#101216", // 0 app background
		"#16181d", // 1 panel
		"#1d2026", // 2 element
		"#2b2f36", // 3 subtle border
		"#3d424b", // 4 border
		"#565c67", // 5 divider
		"#7b828e", // 6 subtle text
		"#a0a7b3", // 7 muted text
		"#ced4dd", // 8 text
		"#eef1f5", // 9 strong text
	},
	Primary:  "#7aa2f7",
	Accent:   "#bb9af7",
	Success:  "#9ece6a",
	Warning:  "#e0af68",
	Danger:   "#f7768e",
	Info:     "#7dcfff",
	OnAccent: "#101216",
}

// LightPalette is the raw ramp behind Light.
var LightPalette = Palette{
	Neutral: [10]string{
		"#fcfcfd", // 0 app background
		"#f4f5f7", // 1 panel
		"#eaebee", // 2 element
		"#dee0e4", // 3 subtle border
		"#c6c9cf", // 4 border
		"#9ca0a9", // 5 divider
		"#767a84", // 6 subtle text
		"#585c66", // 7 muted text
		"#2c2f36", // 8 text
		"#171a1f", // 9 strong text
	},
	Primary:  "#2f5ea8",
	Accent:   "#7048b6",
	Success:  "#3a6f22",
	Warning:  "#8a6100",
	Danger:   "#b02a44",
	Info:     "#1c6f96",
	OnAccent: "#fbfcfe",
}

// tokens maps a raw palette onto the semantic layer. This is the single place
// that decides which ramp step means "panel" or "muted text", so both built-in
// themes stay structurally identical and only their colors differ.
func tokens(p Palette) Tokens {
	n := func(i int) color.Color { return lipgloss.Color(p.Neutral[i]) }

	return Tokens{
		Background:        n(0),
		BackgroundPanel:   n(1),
		BackgroundElement: n(2),

		Text:       n(8),
		TextMuted:  n(7),
		TextSubtle: n(6),
		OnAccent:   lipgloss.Color(p.OnAccent),

		BorderSubtle: n(3),
		Border:       n(4),
		Divider:      n(5),
		BorderActive: lipgloss.Color(p.Primary),

		Primary: lipgloss.Color(p.Primary),
		Accent:  lipgloss.Color(p.Accent),
		Success: lipgloss.Color(p.Success),
		Warning: lipgloss.Color(p.Warning),
		Danger:  lipgloss.Color(p.Danger),
		Info:    lipgloss.Color(p.Info),

		DiffAdded:      lipgloss.Color(p.Success),
		DiffRemoved:    lipgloss.Color(p.Danger),
		DiffContext:    n(7),
		DiffHunkHeader: n(6),
	}
}

// New assembles a Theme from a raw palette. Both built-in themes route through
// here, so a new derived style is added once and both get it.
//
// Content styles set a foreground and never a background. This is a
// correctness rule, not a preference: Lip Gloss terminates every styled run
// with a full SGR reset, and a reset inside a container clears the container's
// background for everything after it. A text style that set its own background
// therefore paints a band that ends wherever the text ends, leaving the rest of
// the row on the terminal's background — which is precisely the patchwork this
// rule exists to prevent. Surfaces are painted by the container that owns them;
// text only ever contributes color.
//
// The two deliberate exceptions are Action and ActionFocused, which are filled
// controls rather than runs of text: each is a single self-contained run whose
// reset lands at its own end, so it cannot bleed into anything.
func New(name string, p Palette) Theme {
	c := tokens(p)

	fg := func(v color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(v) }
	base := fg(c.Text)

	return Theme{
		Name:      name,
		C:         c,
		GaugeRamp: DefaultGaugeRamp,
		Default:   base,
		Normal:    base,
		Bold:      base.Bold(true),
		Italic:    base.Italic(true),
		Code:      fg(c.Info),
		Dim:       fg(c.TextSubtle),
		Error:     fg(c.Danger).Bold(true),
		Warning:   fg(c.Warning),
		Success:   fg(c.Success),

		CodeBlock:     fg(c.TextMuted),
		Border:        fg(c.Border),
		BorderFocused: fg(c.BorderActive),
		RegionTitle:   fg(c.TextMuted).Bold(true),
		Action:        fg(c.Primary),
		ActionFocused: lipgloss.NewStyle().Foreground(c.OnAccent).Background(c.Primary).Bold(true),
		DiffAdd:       fg(c.DiffAdded),
		DiffRemove:    fg(c.DiffRemoved),
		DiffHeader:    fg(c.DiffHunkHeader).Bold(true),
		TableHeader:   fg(c.Text).Bold(true),
		Link:          fg(c.Info).Underline(true),
		SubSession:    fg(c.TextMuted).Italic(true),
	}
}

// Dark returns the built-in dark theme.
func Dark() Theme { return New("dark", DarkPalette) }

// Light returns the built-in light theme.
func Light() Theme { return New("light", LightPalette) }

// ByName resolves a configured theme name. An unknown name resolves to Dark
// with ok false, so a caller can log the fallback rather than failing startup
// over a cosmetic setting.
func ByName(name string) (Theme, bool) {
	switch name {
	case "dark", "":
		return Dark(), true
	case "light":
		return Light(), true
	default:
		return Dark(), false
	}
}
