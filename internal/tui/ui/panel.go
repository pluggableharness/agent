package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/pluggableharness/agent/internal/tui/theme"
)

const (
	// minPanelWidth is the narrowest a panel can be and still have an interior:
	// two border columns plus one content column.
	minPanelWidth = 3
	// panelChromeRows is the two rows a panel spends on its own border.
	panelChromeRows = 2
)

// Panel is a titled, bordered surface.
//
// The title sits in the top border rather than on its own row, which buys back
// a line of content per pane and is what keeps a stack of small side panels
// affordable. A focused panel is distinguished by border color alone — moving
// the border weight instead would reflow the layout on every focus change.
type Panel struct {
	Title string
	Body  string
	// Caption is optional text embedded in the *bottom* border, against the
	// right corner.
	//
	// It is the counterweight to Title. A panel often has two things to say
	// about itself — what it is, and how it is currently configured — and
	// running both along the top border makes one long string in which neither
	// is findable. Splitting them across the diagonal gives each a corner, so
	// the eye learns where to look for which. Like the title it costs no
	// interior row.
	Caption string
	// Width and Height are the panel's outer dimensions, borders included.
	Width  int
	Height int
	// Focused draws the border in the active color.
	Focused bool
	// Accent overrides the title color. Nil uses the muted text token.
	Accent color.Color
	// CaptionAccent overrides the caption color. Nil uses the subtle text
	// token, which is dimmer than the title's default: a caption is reference
	// detail, and it should read as chrome rather than compete with the title.
	CaptionAccent color.Color
	// Border overrides the border language. The zero value uses rounded.
	Border *lipgloss.Border
}

// Render draws the panel and returns exactly Height lines of exactly Width
// cells, so a caller can place it without measuring.
func (p Panel) Render(t theme.Theme) string {
	if p.Width < minPanelWidth || p.Height < 2 {
		// Too small to frame: still cover every cell so the region is painted
		// rather than left showing whatever was there before.
		return FitBlock(p.Body, max(p.Width, 0), max(p.Height, 0), New().Fg(t.C.Text))
	}

	b := theme.BorderRounded
	if p.Border != nil {
		b = *p.Border
	}

	edge := t.C.Border
	if p.Focused {
		edge = t.C.BorderActive
	}

	// Borders and interior carry no background of their own: the application
	// surface is set once on the Bubble Tea View, and a container that filled
	// its own background would only stay filled until the first styled run
	// inside it emitted its terminating reset.
	borderStyle := New().Fg(edge)
	inner := p.Width - 2
	bodyHeight := p.Height - panelChromeRows

	rows := make([]string, 0, p.Height)
	rows = append(rows, p.top(t, b, borderStyle, inner))

	bodyStyle := New().Fg(t.C.Text)
	contentWidth := max(inner-2*theme.Space1, 0)
	pad := strings.Repeat(" ", theme.Space1)

	// A two-row panel is all border and has no interior. Splitting an empty
	// block would still yield one line, so the body is skipped outright rather
	// than trusted to produce none.
	if bodyHeight > 0 {
		for line := range strings.SplitSeq(FitBlock(p.Body, contentWidth, bodyHeight, bodyStyle), "\n") {
			rows = append(rows,
				borderStyle.Render(b.Left)+pad+line+pad+borderStyle.Render(b.Right))
		}
	}

	rows = append(rows, p.bottom(t, b, borderStyle, inner))

	return strings.Join(rows, "\n")
}

// bottom renders the closing border with the caption embedded against the
// right corner, mirroring how top embeds the title against the left.
func (p Panel) bottom(t theme.Theme, b lipgloss.Border, borderStyle Style, inner int) string {
	label := p.captionLabel(inner)
	if label == "" {
		return borderStyle.Render(b.BottomLeft + strings.Repeat(b.Bottom, inner) + b.BottomRight)
	}

	accent := t.C.TextSubtle
	if p.CaptionAccent != nil {
		accent = p.CaptionAccent
	}

	rest := max(inner-1-lipgloss.Width(label), 0)

	return borderStyle.Render(b.BottomLeft+strings.Repeat(b.Bottom, rest)) +
		New().Fg(accent).Render(label) +
		borderStyle.Render(b.Bottom+b.BottomRight)
}

// captionLabel is the caption as it appears in the bottom border, padded.
//
// Unlike the title it is never clipped. A truncated model name ("claude-op…")
// is worse than no model name, because the operator cannot tell which model it
// abbreviates — and unlike a title, the caption is not what identifies the
// panel, so losing it costs nothing. It renders whole or not at all, the same
// way a status segment with no room is dropped rather than shortened.
func (p Panel) captionLabel(inner int) string {
	if p.Caption == "" {
		return ""
	}

	label := " " + p.Caption + " "

	// Leave at least one border cell to the left of the label, so a caption
	// that only just fits still reads as sitting in a border rather than
	// having replaced it.
	if lipgloss.Width(label) > inner-2 {
		return ""
	}

	return label
}

// top builds the top border with the title embedded in it.
func (p Panel) top(t theme.Theme, b lipgloss.Border, borderStyle Style, inner int) string {
	if p.Title == "" || inner < 4 {
		return borderStyle.Render(b.TopLeft + strings.Repeat(b.Top, inner) + b.TopRight)
	}

	accent := t.C.TextMuted
	if p.Accent != nil {
		accent = p.Accent
	}

	label := p.titleLabel(inner)
	titleStyle := New().Fg(accent).Bold()
	rest := max(inner-1-lipgloss.Width(label), 0)

	return borderStyle.Render(b.TopLeft+b.Top) +
		titleStyle.Render(label) +
		borderStyle.Render(strings.Repeat(b.Top, rest)+b.TopRight)
}

// titleLabel is the title as it appears in the top border, padded and clipped.
func (p Panel) titleLabel(inner int) string {
	if p.Title == "" || inner < 4 {
		return ""
	}

	return " " + Fit(p.Title, min(lipgloss.Width(p.Title), inner-4)) + " "
}

// Badge is a small filled label used for status pills in bars.
func Badge(t theme.Theme, text string, fg color.Color) string {
	return New().Fg(t.C.OnAccent).Bg(fg).Bold().Px(theme.Space1).Render(text)
}
