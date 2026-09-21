package tui

import (
	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
)

// activityMirror is the TUI's client-side mirror of the server-owned activity
// store (favorites plus derived recent). It is replaced wholesale by every
// state.changed snapshot; the server is authoritative.
type activityMirror struct {
	favorites []api.Item
	recent    []api.RecentEntry
}

// FavoritesFor returns one source's favorites, newest first.
func (a *activityMirror) FavoritesFor(source string) []core.Item {
	if a == nil {
		return nil
	}
	items := []core.Item{}
	for _, favorite := range a.favorites {
		if string(favorite.Source) != source {
			continue
		}
		items = append(items, apiItemToCore(favorite))
	}
	return items
}

// RecentFor returns one source's derived recent rows, newest first.
func (a *activityMirror) RecentFor(source string) []core.Item {
	if a == nil {
		return nil
	}
	items := []core.Item{}
	for _, entry := range a.recent {
		if string(entry.Item.Source) != source {
			continue
		}
		items = append(items, apiItemToCore(entry.Item))
	}
	return items
}

// IsFavorite reports whether a stable id is favorited.
func (a *activityMirror) IsFavorite(source, id string) bool {
	if a == nil {
		return false
	}
	for _, favorite := range a.favorites {
		if string(favorite.Source) == source && favorite.ID == id {
			return true
		}
	}
	return false
}
