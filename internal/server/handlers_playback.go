package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/player"
)

type playParams struct {
	Ref          string `json:"ref"`
	Name         string `json:"name"`
	Shuffle      *bool  `json:"shuffle"`
	Repeat       string `json:"repeat"`
	StartAt      int    `json:"startAt"`
	StartTrackID string `json:"startTrackID"`
	Reverse      bool   `json:"reverse"`
	FromHere     bool   `json:"fromHere"`
}

func (s *Server) handlePlay(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params playParams
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	reference, refErr := api.ParseReference(params.Ref)
	if refErr != nil {
		return nil, refErr
	}
	// Capability declaration is the routing truth: a source that does not
	// declare playback is unsupported even if an engine is available.
	descriptor, ok := s.descriptorFor(ctx, reference.Source)
	if !ok {
		return nil, api.Errorf(api.CodeSourceUnavailable, "playback is not available for %s", reference.Source)
	}
	radioStream := reference.Source == api.SourceRadio && reference.Kind == api.KindStream
	if radioStream {
		if !declaresCapability(descriptor, api.CapPlaybackStream) {
			return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not support stream playback", reference.Source)
		}
	} else if !declaresCapability(descriptor, api.CapPlaybackFull) && !declaresCapability(descriptor, api.CapPlaybackPreview) {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not support playback", reference.Source)
	}
	// A form the source never declared cannot be honored: refuse before starting
	// instead of reporting success and dropping it. Silently ignoring the
	// parameter made the caller believe shuffle/repeat were on.
	if err := unsupportedForm(descriptor, reference.Source, params.Shuffle, params.Repeat); err != nil {
		return nil, err
	}
	preparer, urlPlayback := s.providers[reference.Source].(PlaybackPreparer)
	if urlPlayback && !s.urlPlaybackAvailable() {
		return nil, api.Errorf(api.CodeSourceUnavailable, "direct URL playback is unavailable")
	}
	transport := transportEngine
	if radioStream {
		transport = transportStream
	} else if urlPlayback {
		transport = transportURLQueue
	}
	if err := s.selectHelperLocked(ctx, transport); err != nil {
		return nil, err
	}
	s.beginPlaybackStartLocked(reference.Source, transport)
	if urlPlayback {
		s.stopICY()
	} else if !radioStream {
		s.stopURLTransportLocked(ctx)
	}
	// A new playback starts from a known form, and the form is applied before the
	// queue is built: MusicKit keeps shuffle/repeat across plays, so an omitted
	// parameter used to inherit the previous playback's form, and changing the
	// form afterwards rebuilt a freshly filled queue down to one entry
	// (batch 2026-09-20-form-and-playlist-fixes r3).
	shuffle, repeat := playForm(params.Shuffle, params.Repeat)
	applied, optionsErr := s.applyFormLocked(ctx, &shuffle, repeat)
	if optionsErr != nil && params.Shuffle == nil && params.Repeat == "" {
		// Nothing was requested, so clearing an inherited form is best effort: a
		// failure here must not turn a plain play into partial_failure.
		optionsErr = nil
		applied = map[string]any{}
	}
	var state core.PlaybackState
	var err error
	queueChanged := true
	// Resolved items flow from the container expansion into the preparer so it
	// does not re-resolve every ref (one page/API round trip per track).
	var resolvedItems []api.Item
	switch {
	case radioStream:
		if s.audioEngine == nil {
			return nil, api.Errorf(api.CodeSourceUnavailable, "stream playback is unavailable")
		}
		state, err = s.audioEngine.RadioPlay(ctx, reference.URL, params.Name)
		queueChanged = false
	case urlPlayback:
		if s.urlTransport == nil {
			return nil, api.Errorf(api.CodeSourceUnavailable, "direct URL playback is unavailable")
		}
		references := []api.Reference{reference}
		startIndex := params.StartAt
		// An album is expanded into its songs before the provider sees it: a
		// URL-queue preparer prepares song refs, and this branch runs ahead of
		// the album branch below. Without it, Apple albums on a URL-queue
		// platform were handed over whole and refused with "needs song
		// references" (the terminal reported it as a bad reference).
		if reference.Kind == api.KindAlbum || reference.Kind == api.KindPlaylist {
			refs, _, start, resolved, expandErr := s.containerSongRefs(ctx, reference, params)
			if expandErr != nil {
				return nil, s.failPlaybackStartLocked(ctx, expandErr)
			}
			references = make([]api.Reference, 0, len(refs))
			for _, raw := range refs {
				parsed, refErr := api.ParseReference(raw)
				if refErr != nil {
					return nil, s.failPlaybackStartLocked(ctx, refErr)
				}
				references = append(references, parsed)
			}
			startIndex = start
			resolvedItems = resolved
		}
		plan, prepareErr := preparer.PreparePlayback(ctx, PlaybackRequest{References: references, StartIndex: startIndex, FromHere: params.FromHere, ResolvedItems: resolvedItems})
		if prepareErr != nil {
			return nil, s.failPlaybackStartLocked(ctx, prepareErr)
		}
		state, err = s.urlTransport.Start(ctx, plan, s.playbackGeneration, s.transportSessionID)
	case reference.Kind == api.KindAlbum:
		// Albums are expanded into their songs server-side, then started
		// through the one-shot queue assignment (playSongs). MusicKit's batch
		// prepare rejects a few albums' content with Code=6 — falsified as a
		// general album limitation on 2026-09-22 (OQ1 probes: four real albums
		// one-shot fine and jump) — so a rejected batch falls back to the
		// start-then-paced-append path that always plays.
		refs, ids, start, _, expandErr := s.containerSongRefs(ctx, reference, params)
		if expandErr != nil {
			return nil, s.failPlaybackStartLocked(ctx, expandErr)
		}
		// Assign to the outer state: a "state, fill, fillErr :=" here would
		// shadow it, and the successful path would commit an empty state while
		// the queue really played (caught by the OQ17 real-session check).
		fill := fillReport{}
		state, fill, err = s.startFiniteQueueLocked(ctx, refs, ids, start)
		if errors.Is(err, errQueueReadyNotPlaying) {
			return nil, s.queueReadyNotPlayingLocked(state, err, queueChanged)
		}
		if err == nil && fill.Skipped > 0 {
			return nil, s.partialFillLocked(state, fill, queueChanged)
		}
	default:
		state, err = s.engine.PlayState(ctx, core.PlaybackRequest{
			Kind: reference.Kind, ID: reference.ID, URL: reference.URL,
			StartAt: params.StartAt, StartTrackID: params.StartTrackID, Reverse: params.Reverse, FromHere: params.FromHere,
		})
	}
	if err != nil {
		return nil, s.failPlaybackStartLocked(ctx, err)
	}
	if radioStream {
		s.startICY(reference.URL)
	} else {
		s.stopICY()
	}
	state = appliedState(applied, state)
	persistErr := s.persistPlaybackSourceLocked(reference.Source)
	projected := s.commitPlaybackLocked(state, queueChanged)
	s.recordAfterPlayLocked(reference, state, params.Name)
	if optionsErr != nil {
		return nil, api.Errorf(api.CodePartialFailure, "playback started but playback options failed").
			WithDetails(map[string]any{"state": projected, "applied": applied})
	}
	if persistErr != nil {
		return nil, api.Errorf(api.CodePartialFailure, "playback started but its source was not saved").
			WithDetails(map[string]any{"state": projected})
	}
	return projected, nil
}

type playSongsParams struct {
	Refs       []string `json:"refs"`
	StartIndex int      `json:"startIndex"`
	Shuffle    *bool    `json:"shuffle"`
	Repeat     string   `json:"repeat"`
}

// unsupportedForm rejects shuffle/repeat requested from a source that never
// declared the capability. Capability is the routing truth, so a form the
// source cannot provide is unsupported_command rather than a silent drop; a
// declared capability that fails while applying still reports partial_failure
// after playback started.
func unsupportedForm(descriptor api.SourceDescriptor, source api.SourceID, shuffle *bool, repeat string) *api.Error {
	if shuffle != nil && !declaresCapability(descriptor, api.CapShuffle) {
		return api.Errorf(api.CodeUnsupportedCommand, "%s does not support shuffle", source)
	}
	if repeat != "" && !declaresCapability(descriptor, api.CapRepeat) {
		return api.Errorf(api.CodeUnsupportedCommand, "%s does not support repeat", source)
	}
	return nil
}

func (s *Server) handlePlaySongs(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params playSongsParams
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if len(params.Refs) == 0 {
		return nil, api.Errorf(api.CodeInvalidRequest, "playSongs requires at least one ref")
	}
	var source api.SourceID
	ids := make([]string, 0, len(params.Refs))
	for _, raw := range params.Refs {
		reference, refErr := api.ParseReference(raw)
		if refErr != nil {
			return nil, refErr
		}
		if reference.Source == api.SourceRadio {
			return nil, api.Errorf(api.CodeSourceMismatch, "radio has no finite queue")
		}
		if source == "" {
			source = reference.Source
		} else if reference.Source != source {
			return nil, api.Errorf(api.CodeSourceMismatch, "refs span multiple sources")
		}
		ids = append(ids, reference.ID)
	}
	descriptor, ok := s.descriptorFor(ctx, source)
	if !ok {
		return nil, api.Errorf(api.CodeSourceUnavailable, "playback is not available for %s", source)
	}
	if !declaresCapability(descriptor, api.CapPlaybackFull) {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not support finite playback", source)
	}
	if err := unsupportedForm(descriptor, source, params.Shuffle, params.Repeat); err != nil {
		return nil, err
	}
	preparer, urlPlayback := s.providers[source].(PlaybackPreparer)
	if urlPlayback && !s.urlPlaybackAvailable() {
		return nil, api.Errorf(api.CodeSourceUnavailable, "direct URL playback is unavailable")
	}
	transport := transportEngine
	if urlPlayback {
		transport = transportURLQueue
	}
	if err := s.selectHelperLocked(ctx, transport); err != nil {
		return nil, err
	}
	s.beginPlaybackStartLocked(source, transport)
	if urlPlayback {
		s.stopICY()
	} else {
		s.stopURLTransportLocked(ctx)
	}
	shuffle, repeat := playForm(params.Shuffle, params.Repeat)
	applied, optionsErr := s.applyFormLocked(ctx, &shuffle, repeat)
	if optionsErr != nil && params.Shuffle == nil && params.Repeat == "" {
		optionsErr = nil
		applied = map[string]any{}
	}
	var state core.PlaybackState
	var err error
	if urlPlayback {
		refs := make([]api.Reference, 0, len(params.Refs))
		for _, raw := range params.Refs {
			ref, _ := api.ParseReference(raw)
			refs = append(refs, ref)
		}
		plan, prepareErr := preparer.PreparePlayback(ctx, PlaybackRequest{References: refs, StartIndex: params.StartIndex})
		if prepareErr != nil {
			return nil, s.failPlaybackStartLocked(ctx, prepareErr)
		}
		state, err = s.urlTransport.Start(ctx, plan, s.playbackGeneration, s.transportSessionID)
	} else {
		// The one-shot assignment (playSongs) is the primary start: it makes
		// the queue jumpable — MusicKit cannot rebuild an append-built queue —
		// and starts without a paced fill. When MusicKit refuses to prepare the
		// batch (Code=6 on some content), fall back to the proven single-play
		// start plus paced appends; a song the engine refuses to queue is
		// skipped and reported.
		start := params.StartIndex
		if start < 0 || start >= len(ids) {
			start = 0
		}
		fill := fillReport{}
		state, fill, err = s.startFiniteQueueLocked(ctx, params.Refs, ids, start)
		if errors.Is(err, errQueueReadyNotPlaying) {
			return nil, s.queueReadyNotPlayingLocked(state, err, true)
		}
		if err == nil && fill.Skipped > 0 {
			return nil, s.partialFillLocked(state, fill, true)
		}
	}
	if err != nil {
		return nil, s.failPlaybackStartLocked(ctx, err)
	}
	s.stopICY()
	state = appliedState(applied, state)
	persistErr := s.persistPlaybackSourceLocked(source)
	projected := s.commitPlaybackLocked(state, true)
	if optionsErr != nil {
		return nil, api.Errorf(api.CodePartialFailure, "playback started but playback options failed").
			WithDetails(map[string]any{"state": projected, "applied": applied})
	}
	if persistErr != nil {
		return nil, api.Errorf(api.CodePartialFailure, "playback started but its source was not saved").
			WithDetails(map[string]any{"state": projected})
	}
	return projected, nil
}

// startFiniteQueueLocked starts a finite MusicKit queue: the one-shot
// assignment first, and the paced-append orchestration as the fallback when
// the engine refuses the batch. One assignment is what keeps the queue
// rebuildable for an Up Next jump; the append path trades that for guaranteed
// audio on content MusicKit will not prepare in one batch (OQ1 probes,
// 2026-09-22).
func (s *Server) startFiniteQueueLocked(ctx context.Context, refs, ids []string, start int) (core.PlaybackState, fillReport, error) {
	if oneshot, err := s.engine.PlaySongs(ctx, core.PlaySongsRequest{IDs: ids, StartAt: start}); err == nil {
		return oneshot, fillReport{Total: len(ids), Added: len(ids)}, nil
	}
	return s.startEngineQueueLocked(ctx, refs, ids, start)
}

// startEngineQueueLocked starts a finite MusicKit queue: the selected song
// through the proven single-play path, then the rest through paced enqueue
// appends. MusicKit parks the player while the first track is starting and
// wedges when entries arrive in that window (a wedge leaves the queue fully
// built but playback never starts), so this waits for an actually playing state
// before inserting and re-pins the player if the paced fill left it stopped.
// fillReport is what a paced fill produced: how many entries were appended and
// how many the engine refused. A refused entry is reported instead of silently
// dropped.
type fillReport struct {
	Added   int
	Skipped int
	Total   int
}

func (s *Server) startEngineQueueLocked(ctx context.Context, refs []string, ids []string, start int) (core.PlaybackState, fillReport, error) {
	if s.engine == nil {
		return core.PlaybackState{}, fillReport{}, errors.New("no playback engine is attached")
	}
	state, err := s.engine.PlayState(ctx, core.PlaybackRequest{Kind: api.KindSong, ID: ids[start], Ref: refs[start]})
	if err != nil {
		return core.PlaybackState{}, fillReport{}, err
	}
	for attempt := 0; attempt < 24; attempt++ {
		probe, probeErr := s.engine.State(ctx)
		if probeErr == nil && probe.Status == "playing" && len(probe.Queue) > 0 {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
	}
	report := fillReport{Total: len(ids)}
	for i, id := range ids {
		if i == start {
			report.Added++
			continue
		}
		queued, enqueueErr := s.engine.Enqueue(ctx, core.PlaybackRequest{Kind: api.KindSong, ID: id, Ref: refs[i]}, "append")
		if enqueueErr != nil {
			// The engine refused this entry. Keep going, but report it: the
			// caller turns a partial fill into partial_failure with the counts.
			report.Skipped++
			continue
		}
		state = queued
		report.Added++
		// Publish progress so a client can show 9/16 instead of an indefinite
		// "working" for the whole fill. queueChanged is true: the queue grew.
		progress := state
		progress.QueueFill = &core.QueueFill{Queued: report.Added, Total: report.Total}
		s.commitPlaybackLocked(progress, true)
		// Pacing: back-to-back inserts wedge the MusicKit player; the manual
		// queue-add flow that works always had seconds between inserts. Keep a
		// conservative gap; bounded by the 45s budget. The interval is
		// probeable through LILT_QUEUE_PACING_MS (measured 2026-09-20: 700ms
		// passed 9/9, 400ms failed 6/10 in a time-clustered round, so the
		// default stays).
		select {
		case <-ctx.Done():
		case <-time.After(s.queuePacing):
		}
	}
	// The paced inserts can outlast MusicKit's starting window and leave the
	// player parked on a stopped/paused snapshot with the track set (batch
	// 2026-09-19-watch-sync-recheck NEW-M3: a fully filled queue ended
	// "Stopped"). The caller asked for playback, so re-pin it; if the player
	// refuses to start at all, report that instead of committing a stopped state
	// that still shows a full queue (batch 2026-09-20-form-fix-recheck OQ13).
	if final, stateErr := s.engine.State(ctx); stateErr == nil && state.Track != nil && (final.Status == "stopped" || final.Status == "paused") {
		resumed, resumeErr := s.engine.ResumeState(ctx)
		if resumeErr != nil {
			// The fill succeeded and MusicKit holds the queue; only starting it
			// failed. Return the built queue with a sentinel so the caller keeps
			// it and reports a recoverable failure instead of discarding a
			// complete fill (docs/product/open-questions.md OQ17).
			return final, report, fmt.Errorf("%w (%v)", errQueueReadyNotPlaying, resumeErr)
		}
		state = resumed
	}
	return state, report, nil
}

// errQueueReadyNotPlaying marks a fully built finite queue whose player refused
// to start. The queue is real and resumable, so it is committed rather than
// thrown away.
var errQueueReadyNotPlaying = errors.New("the queue is ready but playback did not start")

// partialFillLocked commits a queue the engine filled only partially and reports
// the counts, so the user learns that N of M entries are playing instead of
// silently getting a shorter queue. Callers hold s.mu.
func (s *Server) partialFillLocked(state core.PlaybackState, report fillReport, queueChanged bool) *api.Error {
	projected := s.commitPlaybackLocked(state, queueChanged)
	return api.Errorf(api.CodePartialFailure,
		"playback started with %d of %d tracks; the engine refused %d",
		report.Added, report.Total, report.Skipped).
		WithDetails(map[string]any{
			"state":   projected,
			"added":   report.Added,
			"skipped": report.Skipped,
			"total":   report.Total,
		})
}

// queueReadyNotPlayingLocked commits a finite queue whose playback did not start
// and reports it as a partial failure. The queue is kept: the user sees why
// nothing is playing, can press play again, and does not lose the fill. Callers
// hold s.mu.
func (s *Server) queueReadyNotPlayingLocked(state core.PlaybackState, cause error, queueChanged bool) *api.Error {
	state.Error = "the queue is ready but playback did not start; press play to retry"
	if state.Status != "stopped" && state.Status != "paused" {
		state.Status = "paused"
	}
	projected := s.commitPlaybackLocked(state, queueChanged)
	return api.Errorf(api.CodePartialFailure, "%v", cause).
		WithDetails(map[string]any{"state": projected, "queueReady": true})
}

// containerSongRefs expands an album or playlist reference into its song refs
// so the orchestrated finite-queue path can play it. startTrackID wins over
// startAt; fromHere drops the tracks before the selection; reverse flips the
// queue order and mirrors the start point, mirroring the helper's MusicKit
// queue semantics (docs/client-api/commands.md). Container expansion is a
// source concern, so it goes through the provider registry: AlbumProvider and
// PlaylistProvider are the extension points that exist for exactly this, and
// going straight to the MusicKit resource client made album playback
// Apple-on-macOS only, with every other Apple runtime (the browser engine on
// Linux) unable to resolve a track listing.
func (s *Server) containerSongRefs(ctx context.Context, reference api.Reference, params playParams) ([]string, []string, int, []api.Item, *api.Error) {
	provider, ok := s.providers[reference.Source]
	if !ok {
		return nil, nil, 0, nil, api.Errorf(api.CodeSourceUnavailable, "%s playback is not available for %s", reference.Kind, reference.Source)
	}
	var tracks []api.Item
	var providerErr *api.Error
	switch reference.Kind {
	case api.KindAlbum:
		albumProvider, ok := provider.(AlbumProvider)
		if !ok {
			return nil, nil, 0, nil, api.Errorf(api.CodeUnsupportedCommand, "%s cannot resolve album tracks", reference.Source)
		}
		_, tracks, providerErr = albumProvider.AlbumTracks(ctx, reference.ID)
	case api.KindPlaylist:
		playlistProvider, ok := provider.(PlaylistProvider)
		if !ok {
			return nil, nil, 0, nil, api.Errorf(api.CodeUnsupportedCommand, "%s cannot resolve playlist tracks", reference.Source)
		}
		_, tracks, providerErr = playlistProvider.PlaylistTracks(ctx, reference.ID)
	default:
		return nil, nil, 0, nil, api.Errorf(api.CodeInvalidReference, "%s references are not expandable", reference.Kind)
	}
	if providerErr != nil {
		return nil, nil, 0, nil, providerErr
	}
	if len(tracks) == 0 {
		return nil, nil, 0, nil, api.Errorf(api.CodeInvalidReference, "the container has no playable tracks")
	}
	start := 0
	if params.StartTrackID != "" {
		// The wire carries the provider id: clients convert api.Item.ID (the
		// stable "am:1234" spelling) into their own item id through ProviderID,
		// so matching the stable id here never matched a real client's request.
		for i, track := range tracks {
			if track.ProviderID == params.StartTrackID {
				start = i
				break
			}
		}
	} else if params.StartAt > 0 && params.StartAt < len(tracks) {
		start = params.StartAt
	}
	refs := make([]string, 0, len(tracks))
	ids := make([]string, 0, len(tracks))
	for _, track := range tracks {
		// Both spellings the rest of the path needs are the provider id: the
		// canonical ref (source:kind:providerID) and the id handed to the playback
		// backend. The stable id ("am:1234") would produce
		// "apple-music:song:am:1234" — which parses back into an id no provider can
		// resolve — and would ask MusicKit for a resource it does not name.
		refs = append(refs, fmt.Sprintf("%s:%s:%s", reference.Source, api.KindSong, track.ProviderID))
		ids = append(ids, track.ProviderID)
	}
	if params.Reverse {
		for i, j := 0, len(refs)-1; i < j; i, j = i+1, j-1 {
			refs[i], refs[j] = refs[j], refs[i]
			ids[i], ids[j] = ids[j], ids[i]
			tracks[i], tracks[j] = tracks[j], tracks[i]
		}
		start = len(ids) - 1 - start
	}
	if params.FromHere {
		refs, ids, start = refs[start:], ids[start:], 0
		tracks = tracks[start:]
	}
	return refs, ids, start, tracks, nil
}

// playForm resolves the form a new finite-queue playback starts with. MusicKit
// keeps shuffle/repeat across plays, so an omitted parameter used to inherit the
// previous playback's form; a new play therefore always starts from a known
// state and only what the caller asked for differs from it.
func playForm(shuffle *bool, repeat string) (bool, string) {
	effective := false
	if shuffle != nil {
		effective = *shuffle
	}
	mode := "off"
	if repeat != "" {
		mode = repeat
	}
	return effective, mode
}

// applyFormLocked applies shuffle/repeat for the engine transport, returning what
// succeeded. The URL queue transport does not declare those capabilities, so
// nothing is applied for it (and the engine is never touched).
func (s *Server) applyFormLocked(ctx context.Context, shuffle *bool, repeat string) (map[string]any, *api.Error) {
	applied := map[string]any{}
	if s.activeTransport != transportEngine || s.engine == nil {
		return applied, nil
	}
	if shuffle != nil {
		if _, err := s.engine.SetShuffle(ctx, *shuffle); err != nil {
			return applied, s.mapEngineError(err)
		}
		applied["shuffle"] = *shuffle
	}
	if repeat != "" {
		if _, err := s.engine.SetRepeat(ctx, repeat); err != nil {
			return applied, s.mapEngineError(err)
		}
		applied["repeat"] = repeat
	}
	return applied, nil
}

func appliedState(applied map[string]any, fallback core.PlaybackState) core.PlaybackState {
	state := fallback
	if value, ok := applied["shuffle"].(bool); ok {
		state.Shuffle = value
	}
	if value, ok := applied["repeat"].(string); ok {
		state.Repeat = value
	}
	return state
}

func (s *Server) handleTransportControl(operation string) api.Handler {
	return func(ctx context.Context, _ json.RawMessage) (any, *api.Error) {
		if s.usingURLTransportLocked() {
			var state core.PlaybackState
			var err error
			switch operation {
			case "pause":
				state, err = s.urlTransport.Pause(ctx)
			case "resume":
				state, err = s.urlTransport.Resume(ctx)
			case "next":
				state, err = s.urlTransport.Next(ctx)
			case "previous":
				state, err = s.urlTransport.Previous(ctx)
			}
			if err != nil {
				if errors.Is(err, errQueueNoSession) {
					return nil, api.Errorf(api.CodeInvalidState, "nothing is playing")
				}
				if operation == "next" || operation == "previous" {
					if errors.Is(err, errQueueIndexOutOfRange) {
						return nil, api.Errorf(api.CodeInvalidState, "there is no %s track", operation)
					}
					return nil, s.failURLQueueLocked(ctx, err)
				}
				return nil, s.mapEngineError(err)
			}
			return s.commitPlaybackLocked(state, false), nil
		}
		if s.activeTransport == transportStream {
			if s.audioEngine == nil {
				return nil, api.Errorf(api.CodeSourceUnavailable, "stream playback is unavailable")
			}
			var state core.PlaybackState
			var err error
			switch operation {
			case "pause":
				state, err = s.audioEngine.PauseState(ctx)
			case "resume":
				state, err = s.audioEngine.ResumeState(ctx)
			default:
				return nil, api.Errorf(api.CodeUnsupportedCommand, "radio streams do not support %s", operation)
			}
			if err != nil {
				return nil, s.mapEngineError(err)
			}
			return s.commitPlaybackLocked(state, false), nil
		}
		if err := s.requireEngine(); err != nil {
			return nil, err
		}
		var state core.PlaybackState
		var err error
		switch operation {
		case "pause":
			state, err = s.engine.PauseState(ctx)
		case "resume":
			state, err = s.engine.ResumeState(ctx)
		case "next":
			state, err = s.engine.NextState(ctx)
		case "previous":
			state, err = s.engine.PreviousState(ctx)
		}
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		return s.commitPlaybackLocked(state, false), nil
	}
}

func (s *Server) usingURLTransportLocked() bool {
	return s.urlTransport != nil && s.activeTransport == transportURLQueue
}

// urlQueueHasSessionLocked reports whether a live, non-empty URL queue exists.
func (s *Server) urlQueueHasSessionLocked() bool {
	return s.usingURLTransportLocked() && s.urlTransport.List().Source != nil
}

// stopURLTransportLocked best-effort stops any active URL session.
func (s *Server) stopURLTransportLocked(ctx context.Context) {
	if s.urlTransport != nil {
		_, _ = s.urlTransport.Stop(ctx)
	}
}

// failURLQueueLocked ends a URL session after a mid-queue failure: the source
// becomes unavailable, the queue is cleared, and the public state is stopped.
func (s *Server) failURLQueueLocked(ctx context.Context, err error) *api.Error {
	if s.urlTransport != nil {
		_, _ = s.urlTransport.Stop(ctx)
	}
	stopped := core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}
	projected := s.commitPlaybackLocked(stopped, true)
	return api.Errorf(api.CodeSourceUnavailable, "playback stopped: %v", err).
		WithDetails(map[string]any{"state": projected})
}

func (s *Server) handleStop(ctx context.Context, _ json.RawMessage) (any, *api.Error) {
	if s.usingURLTransportLocked() {
		hadQueue := s.urlQueueHasSessionLocked()
		state, err := s.urlTransport.Stop(ctx)
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		return s.commitPlaybackLocked(state, hadQueue), nil
	}
	if s.activeTransport == transportStream {
		if s.audioEngine == nil {
			return s.commitPlaybackLocked(core.PlaybackState{Status: "stopped", Mode: "none", QueueIndex: -1}, true), nil
		}
		state, err := s.audioEngine.RadioStop(ctx)
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		s.stopICY()
		return s.commitPlaybackLocked(state, true), nil
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	state, err := s.engine.Stop(ctx)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	s.stopICY()
	return s.commitPlaybackLocked(state, true), nil
}

func (s *Server) handleToggle(ctx context.Context, _ json.RawMessage) (any, *api.Error) {
	if s.usingURLTransportLocked() {
		current, err := s.urlTransport.State(ctx)
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		var next core.PlaybackState
		if current.Status == "paused" {
			next, err = s.urlTransport.Resume(ctx)
		} else if current.Status == "playing" || current.Status == "buffering" {
			next, err = s.urlTransport.Pause(ctx)
		} else {
			return nil, api.Errorf(api.CodeInvalidState, "nothing is playing to toggle")
		}
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		return s.commitPlaybackLocked(next, false), nil
	}
	if s.activeTransport == transportStream {
		if s.audioEngine == nil {
			return nil, api.Errorf(api.CodeSourceUnavailable, "stream playback is unavailable")
		}
		current, err := s.audioEngine.State(ctx)
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		var next core.PlaybackState
		if current.Status == "paused" {
			next, err = s.audioEngine.ResumeState(ctx)
		} else {
			next, err = s.audioEngine.PauseState(ctx)
		}
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		return s.commitPlaybackLocked(next, false), nil
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	current, err := s.engine.State(ctx)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	var next core.PlaybackState
	switch current.Status {
	case "playing", "buffering":
		next, err = s.engine.PauseState(ctx)
	case "paused", "ended":
		// "ended" is a finite queue that played out; toggling it starts the
		// queue again rather than reporting that nothing is playing.
		next, err = s.engine.ResumeState(ctx)
	default:
		return nil, api.Errorf(api.CodeInvalidState, "nothing is playing to toggle")
	}
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.commitPlaybackLocked(next, false), nil
}

func (s *Server) handleSetShuffle(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		On bool `json:"on"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	source := s.publicActiveSourceLocked()
	descriptor, ok := s.descriptorFor(ctx, source)
	if !ok || !declaresCapability(descriptor, api.CapShuffle) {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not support shuffle", source)
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	state, err := s.engine.SetShuffle(ctx, params.On)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.commitPlaybackLocked(state, false), nil
}

func (s *Server) handleSetRepeat(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		Mode string `json:"mode"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Mode != "off" && params.Mode != "all" && params.Mode != "one" {
		return nil, api.Errorf(api.CodeInvalidRequest, "repeat mode must be off, all, or one")
	}
	source := s.publicActiveSourceLocked()
	descriptor, ok := s.descriptorFor(ctx, source)
	if !ok || !declaresCapability(descriptor, api.CapRepeat) {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not support repeat", source)
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	state, err := s.engine.SetRepeat(ctx, params.Mode)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.commitPlaybackLocked(state, false), nil
}

func (s *Server) handleQueueList(ctx context.Context, _ json.RawMessage) (any, *api.Error) {
	if s.activeTransport == transportURLQueue && s.urlTransport != nil {
		// Public concurrency uses the server-global queue revision, so present
		// that instead of the transport-local counter.
		queue := s.urlTransport.List()
		queue.QueueRevision = s.queueRevision
		return queue, nil
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	state, err := s.engine.State(ctx)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.queueState(state), nil
}

type queueAddParams struct {
	Ref             string  `json:"ref"`
	Position        string  `json:"position"`
	IfQueueRevision *uint64 `json:"ifQueueRevision"`
}

func (s *Server) handleQueueAdd(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params queueAddParams
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Position != "next" && params.Position != "append" {
		return nil, api.Errorf(api.CodeInvalidRequest, "position must be next or append")
	}
	reference, refErr := api.ParseReference(params.Ref)
	if refErr != nil {
		return nil, refErr
	}
	// Route by the ITEM's source, not by the server's last active transport: a
	// URL-source item must never fall through to the MusicKit engine enqueue.
	// When a one-shot batch is rejected the transport goes stale, and the
	// engine path then answered a Jamendo enqueue with a success toast over an
	// empty queue (batch 2026-09-22-recheck2 r4-recheck, fake round).
	if reference.Source == api.SourceRadio {
		return nil, api.Errorf(api.CodeQueueUnavailable, "radio streams have no editable queue")
	}
	if _, urlPlayback := s.providers[reference.Source].(PlaybackPreparer); urlPlayback {
		return s.addURLQueueItem(ctx, params)
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
		return nil, apiErr
	}
	current, err := s.engine.State(ctx)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	if len(current.Queue) > 0 {
		activeSource := s.publicActiveSourceLocked()
		if activeSource != reference.Source {
			return nil, api.Errorf(api.CodeSourceMismatch, "the active queue belongs to %s", activeSource)
		}
	}
	state, err := s.engine.Enqueue(ctx, core.PlaybackRequest{Kind: reference.Kind, ID: reference.ID, URL: reference.URL}, params.Position)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.commitPlaybackLocked(state, true), nil
}

// addURLQueueItem prepares one same-source ref and appends it to the URL queue.
func (s *Server) addURLQueueItem(ctx context.Context, params queueAddParams) (any, *api.Error) {
	if !s.urlQueueHasSessionLocked() {
		return nil, api.Errorf(api.CodeQueueUnavailable, "there is no active URL queue")
	}
	if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
		return nil, apiErr
	}
	reference, refErr := api.ParseReference(params.Ref)
	if refErr != nil {
		return nil, refErr
	}
	source := s.publicActiveSourceLocked()
	if reference.Source != source {
		return nil, api.Errorf(api.CodeSourceMismatch, "the active queue belongs to %s", source)
	}
	preparer, ok := s.providers[source].(PlaybackPreparer)
	if !ok {
		return nil, api.Errorf(api.CodeUnsupportedCommand, "%s does not support an editable queue", source)
	}
	var resolvedItems []api.Item
	if reference.Kind == api.KindAlbum || reference.Kind == api.KindPlaylist {
		refs, _, _, resolved, expandErr := s.containerSongRefs(ctx, reference, playParams{})
		if expandErr != nil {
			return nil, expandErr
		}
		references := make([]api.Reference, 0, len(refs))
		for _, raw := range refs {
			parsed, parseErr := api.ParseReference(raw)
			if parseErr != nil {
				return nil, api.Errorf(api.CodeInvalidReference, "%v", parseErr)
			}
			references = append(references, parsed)
		}
		resolvedItems = resolved
		// A playlist adds its whole content; queue.add has no fromHere.
		plan, prepareErr := preparer.PreparePlayback(ctx, PlaybackRequest{References: references, ResolvedItems: resolvedItems})
		if prepareErr != nil {
			return nil, prepareErr
		}
		queue := plan.PublicQueue()
		if len(queue) == 0 {
			return nil, api.Errorf(api.CodeInvalidReference, "ref has no playable item")
		}
		state, err := s.urlTransport.Add(ctx, queue, params.Position)
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		return s.commitPlaybackLocked(state, true), nil
	}
	plan, prepareErr := preparer.PreparePlayback(ctx, PlaybackRequest{References: []api.Reference{reference}})
	if prepareErr != nil {
		return nil, prepareErr
	}
	queue := plan.PublicQueue()
	if len(queue) == 0 {
		return nil, api.Errorf(api.CodeInvalidReference, "ref has no playable item")
	}
	state, err := s.urlTransport.Add(ctx, queue, params.Position)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.commitPlaybackLocked(state, true), nil
}

type queueIndexParams struct {
	Index           int     `json:"index"`
	IfQueueRevision *uint64 `json:"ifQueueRevision"`
}

func (s *Server) handleQueueJump(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	if s.usingURLTransportLocked() {
		if !s.urlQueueHasSessionLocked() {
			return nil, api.Errorf(api.CodeQueueUnavailable, "there is no active URL queue")
		}
		var p queueIndexParams
		if err := api.DecodeParams(raw, &p); err != nil {
			return nil, err
		}
		if err := s.checkQueueRevision(p.IfQueueRevision); err != nil {
			return nil, err
		}
		state, err := s.urlTransport.Jump(ctx, p.Index)
		if err != nil {
			if errors.Is(err, errQueueNoSession) {
				return nil, api.Errorf(api.CodeQueueUnavailable, "there is no active URL queue")
			}
			if errors.Is(err, errQueueIndexOutOfRange) {
				return nil, api.Errorf(api.CodeInvalidRequest, "queue index is out of range")
			}
			return nil, s.failURLQueueLocked(ctx, err)
		}
		return s.commitPlaybackLocked(state, false), nil
	}
	var p queueIndexParams
	if err := api.DecodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	if err := s.checkQueueRevision(p.IfQueueRevision); err != nil {
		return nil, err
	}
	state, apiErr := s.jumpEngineQueue(ctx, p.Index)
	if apiErr != nil {
		if apiErr.Code == api.CodePreviewUnsupported {
			return nil, api.Errorf(api.CodeQueueUnavailable, "there is no active finite queue")
		}
		return nil, apiErr
	}
	return s.commitPlaybackLocked(state, false), nil
}

// jumpEngineQueue only rebuilds an append-built queue with a one-shot
// assignment. A failed assignment must not silently start a different queue
// via the paced-append fallback used by playback.playSongs. Callers hold s.mu.
func (s *Server) jumpEngineQueue(ctx context.Context, index int) (core.PlaybackState, *api.Error) {
	before, err := s.engine.State(ctx)
	if err != nil {
		return core.PlaybackState{}, s.mapEngineError(err)
	}
	state, jumpErr := s.engine.QueueJump(ctx, index)
	if jumpErr == nil {
		return state, nil
	}
	var refusal *player.RPCError
	if !errors.As(jumpErr, &refusal) || refusal.Code != "queue_not_jumpable" ||
		index < 0 || index >= len(before.Queue) {
		return core.PlaybackState{}, s.mapEngineError(jumpErr)
	}
	// A helper can refuse after touching its queue. Check before starting any
	// second operation; the original refusal alone cannot prove playback kept
	// going. On uncertainty, tell the caller to read the current state.
	if ctx.Err() != nil {
		return core.PlaybackState{}, s.queueJumpOutcomeUnknownLocked()
	}
	afterJump, stateErr := s.engine.State(ctx)
	if stateErr != nil {
		s.mapEngineError(stateErr)
		return core.PlaybackState{}, s.queueJumpOutcomeUnknownLocked()
	}
	if !sameJumpPlayback(before, afterJump) {
		return core.PlaybackState{}, s.reportIncompleteJumpLocked(before, afterJump)
	}
	ids := make([]string, len(before.Queue))
	for i, item := range before.Queue {
		identity := api.NewIdentity(api.SourceAppleMusic, item.Kind, item.ID, item.URL)
		if identity.Source != api.SourceAppleMusic || identity.ProviderID == "" {
			return core.PlaybackState{}, s.queueNotJumpableLocked(before)
		}
		ids[i] = identity.ProviderID
	}
	// playSongs itself preserves the engine's shuffle and repeat. Unlike a new
	// playback start, a jump must not reset the form or try a paced-append start
	// if MusicKit refuses this one-shot assignment.
	if _, rebuildErr := s.engine.PlaySongs(ctx, core.PlaySongsRequest{IDs: ids, StartAt: index}); rebuildErr != nil {
		if player.IsTransportError(rebuildErr) {
			s.mapEngineError(rebuildErr)
			return core.PlaybackState{}, s.queueJumpOutcomeUnknownLocked()
		}
		return core.PlaybackState{}, s.observeRefusedJumpLocked(ctx, before)
	}
	if ctx.Err() != nil {
		return core.PlaybackState{}, s.queueJumpOutcomeUnknownLocked()
	}
	rebuilt, stateErr := s.engine.State(ctx)
	if stateErr != nil {
		s.mapEngineError(stateErr)
		return core.PlaybackState{}, s.queueJumpOutcomeUnknownLocked()
	}
	if !sameQueueComposition(before, rebuilt) || rebuilt.QueueIndex != index ||
		rebuilt.Track == nil || !sameQueueItem(*rebuilt.Track, before.Queue[index]) ||
		(rebuilt.Status != "playing" && rebuilt.Status != "buffering") {
		return core.PlaybackState{}, s.reportIncompleteJumpLocked(before, rebuilt)
	}
	return rebuilt, nil
}

// observeRefusedJumpLocked reconciles a failed one-shot attempt against the
// prior snapshot. No fallback is safe if the queue has already changed.
func (s *Server) observeRefusedJumpLocked(ctx context.Context, before core.PlaybackState) *api.Error {
	if ctx.Err() != nil {
		return s.queueJumpOutcomeUnknownLocked()
	}
	after, err := s.engine.State(ctx)
	if err != nil {
		s.mapEngineError(err)
		return s.queueJumpOutcomeUnknownLocked()
	}
	if !sameJumpPlayback(before, after) {
		return s.reportIncompleteJumpLocked(before, after)
	}
	return s.queueNotJumpableLocked(after)
}

func (s *Server) queueNotJumpableLocked(state core.PlaybackState) *api.Error {
	return api.Errorf(api.CodeQueueNotJumpable,
		"the queue could not be jumped or rebuilt; check the current track, then start the row from its list").
		WithDetails(map[string]any{"state": s.projectState(state, s.publicActiveSourceLocked(), s.sequence, s.queueRevision)})
}

func (s *Server) reportIncompleteJumpLocked(before, after core.PlaybackState) *api.Error {
	if sameJumpPlayback(before, after) {
		return s.queueNotJumpableLocked(after)
	}
	projected := s.commitPlaybackLocked(after, !sameQueueComposition(before, after))
	return api.Errorf(api.CodePartialFailure,
		"the jump did not complete and playback or the queue changed; check the current state before trying again").
		WithDetails(map[string]any{"state": projected})
}

// An unknown mutation may have replaced the queue. Invalidate every client's
// cached index even though the server cannot yet project an authoritative
// playback snapshot. Watch clients receive the warning and refresh on demand.
func (s *Server) queueJumpOutcomeUnknownLocked() *api.Error {
	s.queueRevision++
	s.nextSequenceLocked()
	message := "the jump outcome could not be confirmed; check the current playback state before changing the queue"
	warning := map[string]any{"code": api.CodeOperationOutcomeUnknown, "message": message}
	s.logf("server.warning", warning)
	s.publishLocked("server.warning", warning)
	return api.Errorf(api.CodeOperationOutcomeUnknown, "%s", message).
		WithDetails(map[string]any{"queueRevision": s.queueRevision})
}

func sameQueueItem(a, b core.Item) bool {
	// Apple queue identity is the provider song id and kind. A public URL is
	// display metadata and must not turn a metadata refresh into a queue edit.
	return a.Kind == b.Kind && a.ID == b.ID
}

func sameQueueComposition(a, b core.PlaybackState) bool {
	if len(a.Queue) != len(b.Queue) {
		return false
	}
	for i := range a.Queue {
		if !sameQueueItem(a.Queue[i], b.Queue[i]) {
			return false
		}
	}
	return true
}

func sameJumpPlayback(a, b core.PlaybackState) bool {
	if !sameQueueComposition(a, b) || a.QueueIndex != b.QueueIndex ||
		a.Status != b.Status || a.Mode != b.Mode || a.Shuffle != b.Shuffle || a.Repeat != b.Repeat ||
		(a.Track == nil) != (b.Track == nil) {
		return false
	}
	if a.Track != nil && !sameQueueItem(*a.Track, *b.Track) {
		return false
	}
	// A fresh start on the same track is not an unchanged playback.
	return b.Position >= a.Position-1
}

func (s *Server) handleQueueRemove(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	if s.activeTransport == transportURLQueue {
		if !s.urlQueueHasSessionLocked() {
			return nil, api.Errorf(api.CodeQueueUnavailable, "there is no active URL queue")
		}
		var params queueIndexParams
		if err := api.DecodeParams(raw, &params); err != nil {
			return nil, err
		}
		if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
			return nil, apiErr
		}
		state, err := s.urlTransport.Remove(ctx, params.Index)
		if err != nil {
			if errors.Is(err, errQueueIndexOutOfRange) {
				return nil, api.Errorf(api.CodeInvalidRequest, "queue index is out of range")
			}
			return nil, s.failURLQueueLocked(ctx, err)
		}
		return s.commitPlaybackLocked(state, true), nil
	}
	return s.queueIndexOp(ctx, raw, true, func(index int) (core.PlaybackState, error) {
		return s.engine.QueueRemove(ctx, index)
	})
}

func (s *Server) queueIndexOp(ctx context.Context, raw json.RawMessage, queueChanged bool, call func(int) (core.PlaybackState, error)) (any, *api.Error) {
	var params queueIndexParams
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
		return nil, apiErr
	}
	state, err := call(params.Index)
	if err != nil {
		mapped := s.mapEngineError(err)
		// A queue operation with nothing queued reaches the helper in a
		// non-full mode and comes back as preview_unsupported; report the
		// documented queue error family instead of the preview-control one.
		if mapped.Code == api.CodePreviewUnsupported {
			return nil, api.Errorf(api.CodeQueueUnavailable, "there is no active finite queue")
		}
		return nil, mapped
	}
	return s.commitPlaybackLocked(state, queueChanged), nil
}

type queueMoveParams struct {
	From            int     `json:"from"`
	To              int     `json:"to"`
	IfQueueRevision *uint64 `json:"ifQueueRevision"`
}

func (s *Server) handleQueueMove(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params queueMoveParams
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if s.activeTransport == transportURLQueue {
		if !s.urlQueueHasSessionLocked() {
			return nil, api.Errorf(api.CodeQueueUnavailable, "there is no active URL queue")
		}
		if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
			return nil, apiErr
		}
		state, err := s.urlTransport.Move(ctx, params.From, params.To)
		if err != nil {
			if errors.Is(err, errQueueIndexOutOfRange) {
				return nil, api.Errorf(api.CodeInvalidRequest, "queue index is out of range")
			}
			return nil, s.mapEngineError(err)
		}
		return s.commitPlaybackLocked(state, params.From != params.To), nil
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
		return nil, apiErr
	}
	state, err := s.engine.QueueMove(ctx, params.From, params.To)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.commitPlaybackLocked(state, params.From != params.To), nil
}

func (s *Server) handleQueueClear(ctx context.Context, raw json.RawMessage) (any, *api.Error) {
	var params struct {
		IfQueueRevision *uint64 `json:"ifQueueRevision"`
	}
	if err := api.DecodeParams(raw, &params); err != nil {
		return nil, err
	}
	if s.activeTransport == transportURLQueue {
		if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
			return nil, apiErr
		}
		hadQueue := s.urlQueueHasSessionLocked()
		state, err := s.urlTransport.Stop(ctx)
		if err != nil {
			return nil, s.mapEngineError(err)
		}
		return s.commitPlaybackLocked(state, hadQueue), nil
	}
	if err := s.requireEngine(); err != nil {
		return nil, err
	}
	if apiErr := s.checkQueueRevision(params.IfQueueRevision); apiErr != nil {
		return nil, apiErr
	}
	state, err := s.engine.QueueClear(ctx)
	if err != nil {
		return nil, s.mapEngineError(err)
	}
	return s.commitPlaybackLocked(state, true), nil
}
