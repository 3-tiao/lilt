// Package presentation contains the trust boundary between external metadata
// and terminal rendering.
package presentation

import (
	"strings"
	"unicode"

	"github.com/caiguo/lilt/core"
)

// Text removes terminal control input while preserving printable Unicode and
// spaces. Newlines and tabs are replaced with spaces because all
// current presentation surfaces are single-line rows.
func Text(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteByte(' ')
		case r == 0x1b || r == 0x9b || r < 0x20 || (r >= 0x7f && r <= 0x9f) || unicode.IsControl(r):
			// Dropping ESC, C0, and C1 also makes CSI/OSC payloads inert. Their
			// printable payload remains useful text and cannot control a terminal.
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func Item(item core.Item) core.Item {
	item.Kind = Text(item.Kind)
	item.ID = Text(item.ID)
	item.URL = Text(item.URL)
	item.Title = Text(item.Title)
	item.Artist = Text(item.Artist)
	item.PreviewURL = Text(item.PreviewURL)
	return item
}

func Items(items []core.Item) []core.Item {
	clean := make([]core.Item, len(items))
	for i := range items {
		clean[i] = Item(items[i])
	}
	return clean
}

func Playback(value core.PlaybackState) core.PlaybackState {
	value.Status = Text(value.Status)
	value.Format = Text(value.Format)
	value.Repeat = Text(value.Repeat)
	value.Mode = Text(value.Mode)
	value.Authorization = Text(value.Authorization)
	value.AccountStatus = Text(value.AccountStatus)
	value.AccountError = Text(value.AccountError)
	value.Error = Text(value.Error)
	if value.Track != nil {
		track := Item(*value.Track)
		value.Track = &track
	}
	value.Queue = Items(value.Queue)
	for i := range value.Available {
		value.Available[i] = Text(value.Available[i])
	}
	return value
}
