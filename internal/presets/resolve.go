package presets

import (
	"context"
	"errors"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/state"
)

var ErrNoMatch = errors.New("preset resolved no candidate")

type Provider interface {
	Search(context.Context, string, int) ([]core.Item, error)
	SearchPlaylists(context.Context, string, int) ([]core.Item, error)
	Stations(context.Context, string, int) ([]core.Item, error)
}

func Resolve(ctx context.Context, provider Provider, store *state.Store, preset Preset) (core.Item, error) {
	var (
		items []core.Item
		err   error
	)
	switch preset.Kind {
	case "station":
		items, err = provider.Stations(ctx, preset.Query, 10)
	case "playlist":
		items, err = provider.SearchPlaylists(ctx, preset.Query, 10)
	default:
		items, err = provider.Search(ctx, preset.Query, 10)
	}
	if err != nil {
		return core.Item{}, err
	}
	if store != nil {
		items = store.Rank(preset.Key, items)
	}
	if len(items) == 0 {
		return core.Item{}, ErrNoMatch
	}
	if store != nil {
		store.Record(preset.Key, items[0])
	}
	return items[0], nil
}
