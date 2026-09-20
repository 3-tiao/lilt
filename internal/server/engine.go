package server

import (
	"context"

	"github.com/caiguo/lilt/core"
)

// Engine is the playback and discovery backend the server owns. player.Client
// (the Apple Music helper) and the deterministic fake both implement it.
type Engine interface {
	State(context.Context) (core.PlaybackState, error)
	PlayState(context.Context, core.PlaybackRequest) (core.PlaybackState, error)
	PauseState(context.Context) (core.PlaybackState, error)
	ResumeState(context.Context) (core.PlaybackState, error)
	NextState(context.Context) (core.PlaybackState, error)
	PreviousState(context.Context) (core.PlaybackState, error)
	SetShuffle(context.Context, bool) (core.PlaybackState, error)
	SetRepeat(context.Context, string) (core.PlaybackState, error)
	Stop(context.Context) (core.PlaybackState, error)
	Enqueue(context.Context, core.PlaybackRequest, string) (core.PlaybackState, error)
	QueueJump(context.Context, int) (core.PlaybackState, error)
	QueueRemove(context.Context, int) (core.PlaybackState, error)
	QueueMove(context.Context, int, int) (core.PlaybackState, error)
	QueueClear(context.Context) (core.PlaybackState, error)
	RadioPlay(context.Context, string, string) (core.PlaybackState, error)
	RadioStop(context.Context) (core.PlaybackState, error)
	Probe(context.Context, string, int) (core.RadioProbeResult, error)
	Authorization(context.Context) (core.AuthorizationStatus, error)
	RequestAuthorization(context.Context, bool) (core.AuthorizationStatus, error)
	Search(context.Context, string, int) ([]core.Item, error)
	SearchPlaylists(context.Context, string, int) ([]core.Item, error)
	SearchAlbums(context.Context, string, int) ([]core.Item, error)
	LibraryPlaylists(context.Context) ([]core.Item, error)
	LibraryAlbums(context.Context) ([]core.Item, error)
	PlaylistTracks(context.Context, string) (core.Item, []core.Item, error)
	AlbumTracks(context.Context, string) (core.Item, []core.Item, error)
	RecentPlayed(context.Context, int) ([]core.Item, error)
	Stations(context.Context, string, int) ([]core.Item, error)
	Recommendations(context.Context) ([]core.Item, error)
	ResolveURL(context.Context, string) (core.Item, error)
	SubscribeState(context.Context) (core.StateSubscription, error)
	UnsubscribeState(context.Context) error
}

// AudioEngine is the private lilt-audio capability surface. It deliberately
// excludes MusicKit discovery and queue operations.
type AudioEngine interface {
	State(context.Context) (core.PlaybackState, error)
	PauseState(context.Context) (core.PlaybackState, error)
	ResumeState(context.Context) (core.PlaybackState, error)
	Stop(context.Context) (core.PlaybackState, error)
	RadioPlay(context.Context, string, string) (core.PlaybackState, error)
	RadioStop(context.Context) (core.PlaybackState, error)
	Probe(context.Context, string, int) (core.RadioProbeResult, error)
	SubscribeState(context.Context) (core.StateSubscription, error)
	UnsubscribeState(context.Context) error
}
