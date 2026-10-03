package tui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/3-tiao/lilt/internal/theme"
)

type renderer struct {
	titleStyle, tabStyle, accentStyle            lipgloss.Style
	warnStyle, okStyle, errorStyle               lipgloss.Style
	selStyle, currentStyle                       lipgloss.Style
	trackStyle, rowStyle, dimStyle, loadingStyle lipgloss.Style
	// selection is the raw token cursorFill paints onto a row; the fill marks
	// keyboard focus only, never playback state.
	selection string
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
	r.selection = t.Selection
	r.selStyle = r.cursorFill(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.BrightFG)))
	r.trackStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.BrightFG))
	r.rowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.BrightFG))
	// Muted text is the palette's own fg. Terminal faint on top of a theme's
	// already-dim secondary colour drops it below readability (docs/ui/theme.md).
	r.dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.FG))
	// Playback state is a text colour plus a glyph. The fill belongs to the
	// keyboard cursor alone, so a playing row and a selected row can never be
	// mistaken for each other (docs/ui/design-system.md §4).
	r.currentStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.Green))
	r.loadingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Yellow))
	r.scrollbarStyle = lipgloss.NewStyle().Foreground(border)
	// Borders define structure, not state. They are derived from fg toward bg so
	// the frame stays quieter than the rows it contains in every palette.
	r.border = border
	r.cursor = lipgloss.Color(t.BrightFG)
	r.canvasEscape = backgroundSGR(t.BG)
	return r
}

// cursorFill paints keyboard focus onto a row style. Focus owns the fill and the
// row's own state owns the text colour, so one row can show both at once without
// either token standing in for the other.
func (r renderer) cursorFill(base lipgloss.Style) lipgloss.Style {
	if r.selection != "" {
		return base.Background(lipgloss.Color(r.selection))
	}
	// A palette with neither selection nor bg (the ANSI default) owns no
	// background colour, so reverse video is the only visible cursor.
	return base.Reverse(true)
}

// inputStyles themes the bubbles text input. Its own defaults inherit the
// terminal colours, which disappear as soon as lilt paints a canvas: the search
// query was invisible on the light print-room theme.
func inputStyles(r renderer) textinput.Styles {
	// The placeholder is italic on the muted token: colour alone did not
	// separate "optional station name" from typed text, and the first
	// characters read as an entered query (usability f5). Italic keeps the
	// distinction in every palette, monochrome included.
	placeholder := r.dimStyle.Italic(true)
	return textinput.Styles{
		Focused: textinput.StyleState{Text: r.rowStyle, Placeholder: placeholder, Suggestion: r.dimStyle, Prompt: r.accentStyle},
		Blurred: textinput.StyleState{Text: r.dimStyle, Placeholder: placeholder, Suggestion: r.dimStyle, Prompt: r.dimStyle},
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
