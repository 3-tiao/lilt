package server

import (
	"context"

	"github.com/caiguo/lilt/core"
)

// Engine is the active MusicKit playback backend. Its lifecycle is exclusive
// with AudioEngine because the active backend owns the macOS Now Playing
// session. Discovery, library access, and ref resolution deliberately do not
// belong here; AppleResourceClient serves them independently of playback.
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
	SubscribeState(context.Context) (core.StateSubscription, error)
	UnsubscribeState(context.Context) error
}

// AppleResourceClient is the read-mostly MusicKit surface owned by the Apple
// source provider. It may live while an AudioEngine is playing: it must never
// start audio, mutate a playback queue, or own public playback state.
type AppleResourceClient interface {
	Authorization(context.Context) (core.AuthorizationStatus, error)
	RequestAuthorization(context.Context, bool) (core.AuthorizationStatus, error)
	Search(context.Context, string, int) ([]core.Item, error)
	SearchPlaylists(context.Context, string, int) ([]core.Item, error)
	SearchAlbums(context.Context, string, int) ([]core.Item, error)
	LibraryPlaylists(context.Context) ([]core.Item, error)
	LibraryAlbums(context.Context) ([]core.Item, error)
	PlaylistTracks(context.Context, string) (core.Item, []core.Item, error)
	AlbumTracks(context.Context, string) (core.Item, []core.Item, error)
	Stations(context.Context, string, int) ([]core.Item, error)
	Recommendations(context.Context) ([]core.Item, error)
	// TrackInfo resolves one catalog item's display metadata by stable id. It
	// backs favorites.add; kinds outside the catalog lookup return an error.
	TrackInfo(context.Context, string, string) (core.Item, error)
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
