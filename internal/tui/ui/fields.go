package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/pluggableharness/agent/internal/tui/theme"
)

// Field is one label/value pair in a vertical list.
type Field struct {
	Label string
	Value string
	// Tone colors the value. Nil uses ordinary text.
	Tone color.Color
	// Wide renders the value on its own line beneath the label, for values too
	// long to sit beside one in a narrow panel.
	Wide bool
}

// Fields renders a label/value list with the labels aligned into a column.
//
// This is the shape almost every side panel wants, and aligning the values into
// a common column is what makes such a panel scannable — the eye tracks one
// vertical edge instead of hunting for where each value starts. Fields with no
// value are dropped, on the same principle as a status segment: a panel should
// not advertise what it cannot fill.
func Fields(t theme.Theme, fields []Field, width int) string {
	present := make([]Field, 0, len(fields))

	for _, f := range fields {
		if f.Value != "" {
			present = append(present, f)
		}
	}

	if len(present) == 0 || width <= 0 {
		return ""
	}

	labelWidth := 0
	for _, f := range present {
		if !f.Wide {
			labelWidth = max(labelWidth, lipgloss.Width(f.Label))
		}
	}

	// A label column wider than half the panel is not a column, it is a wall.
	// Past that, everything wraps to its own line instead.
	stacked := labelWidth+2 > width/2

	lines := make([]string, 0, len(present))
	for _, f := range present {
		lines = append(lines, renderField(t, f, width, labelWidth, stacked))
	}

	return strings.Join(lines, "\n")
}

func renderField(t theme.Theme, f Field, width, labelWidth int, stacked bool) string {
	tone := t.C.Text
	if f.Tone != nil {
		tone = f.Tone
	}

	label := New().Fg(t.C.TextSubtle).Render(f.Label)

	if stacked || f.Wide {
		return label + "\n" + New().Fg(tone).Render(Clip(f.Value, width))
	}

	pad := strings.Repeat(" ", max(labelWidth-lipgloss.Width(f.Label), 0)+2)

	return label + pad + New().Fg(tone).Render(Clip(f.Value, max(width-labelWidth-2, 1)))
}
