package tui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/caiguo/lilt/internal/theme"
)

type renderer struct {
	titleStyle, tabStyle, accentStyle            lipgloss.Style
	warnStyle, okStyle, errorStyle               lipgloss.Style
	selStyle, currentStyle                       lipgloss.Style
	trackStyle, rowStyle, dimStyle, loadingStyle lipgloss.Style
	// scrollbarStyle matches the panel border so the gutter stays quiet.
	scrollbarStyle lipgloss.Style
	border         color.Color
	cursor         color.Color
	// canvasEscape fills every cell of the frame with the theme background. It is
	// empty for palettes without a bg key, which keep the terminal's own
	// background (see docs/ui/theme.md).
	canvasEscape string
}

func newRenderer(t theme.Theme) renderer {
	r := renderer{}
	border := lipgloss.Color(theme.Border(t))
	r.titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.Accent))
	r.tabStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.FG))
	r.accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent))
	r.warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Yellow))
	r.okStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Green))
	r.errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Red))
	r.selStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.BrightFG))
	if t.Selection != "" {
		r.selStyle = r.selStyle.Background(lipgloss.Color(t.Selection))
	} else {
		// A palette with neither selection nor bg (the ANSI default) owns no
		// background colour, so reverse video is the only visible cursor.
		r.selStyle = r.selStyle.Reverse(true)
	}
	r.trackStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.BrightFG))
	r.rowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.BrightFG))
	// Muted text is the palette's own fg. Terminal faint on top of a theme's
	// already-dim secondary colour drops it below readability (docs/ui/theme.md).
	r.dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.FG))
	// The playing row is marked by green text and a ▶ marker on the same
	// background as the cursor row: no palette colour is used as a loud fill.
	r.currentStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.Green))
	if t.Selection != "" {
		r.currentStyle = r.currentStyle.Background(lipgloss.Color(t.Selection))
	}
	r.loadingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Yellow))
	r.scrollbarStyle = lipgloss.NewStyle().Foreground(border)
	// Borders define structure, not state. They are derived from fg toward bg so
	// the frame stays quieter than the rows it contains in every palette.
	r.border = border
	r.cursor = lipgloss.Color(t.BrightFG)
	r.canvasEscape = backgroundSGR(t.BG)
	return r
}

// inputStyles themes the bubbles text input. Its own defaults inherit the
// terminal colours, which disappear as soon as lilt paints a canvas: the search
// query was invisible on the light print-room theme.
func inputStyles(r renderer) textinput.Styles {
	return textinput.Styles{
		Focused: textinput.StyleState{Text: r.rowStyle, Placeholder: r.dimStyle, Suggestion: r.dimStyle, Prompt: r.accentStyle},
		Blurred: textinput.StyleState{Text: r.dimStyle, Placeholder: r.dimStyle, Suggestion: r.dimStyle, Prompt: r.dimStyle},
		Cursor:  textinput.CursorStyle{Color: r.cursor, Shape: tea.CursorBlock, Blink: true},
	}
}

// setTheme swaps the renderer and re-themes the text input together, so no code
// path can leave the input on the terminal's own colours.
func (m Model) setTheme(name string) Model {
	m.renderer = newRenderer(theme.Load(name))
	m.input.SetStyles(inputStyles(m.renderer))
	return m
}

// backgroundSGR returns the escape that fills one cell with the canvas colour,
// or "" when the theme keeps the terminal background. Lipgloss exposes no
// accessor for the sequence itself, so it is read off a one-cell render.
func backgroundSGR(value string) string {
	if value == "" {
		return ""
	}
	rendered := lipgloss.NewStyle().Background(lipgloss.Color(value)).Render(" ")
	if end := strings.IndexByte(rendered, ' '); end > 0 {
		return rendered[:end]
	}
	return ""
}

var defaultRenderer = newRenderer(theme.Load(""))
