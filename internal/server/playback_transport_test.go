package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/api"
)

type fakeURLDriver struct {
	targets []URLPlaybackTarget
	pauses  int
	resumes int
	stops   int
}

func (d *fakeURLDriver) PlayURL(_ context.Context, target URLPlaybackTarget) (core.PlaybackState, error) {
	d.targets = append(d.targets, target)
	// Deliberately return the media URL; the transport must strip it.
	return core.PlaybackState{Status: "playing", Mode: "url", Track: &core.Item{URL: target.URL}, Duration: 120}, nil
}
func (d *fakeURLDriver) PauseURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.pauses++
	return core.PlaybackState{Status: "paused", Mode: "url"}, nil
}
func (d *fakeURLDriver) ResumeURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.resumes++
	return core.PlaybackState{Status: "playing", Mode: "url"}, nil
}
func (d *fakeURLDriver) StopURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.stops++
	return core.PlaybackState{Status: "stopped"}, nil
}
func (d *fakeURLDriver) StateURL(context.Context, uint64, string) (core.PlaybackState, error) {
	return core.PlaybackState{Status: "playing", Mode: "url", Track: &core.Item{URL: "https://signed.invalid/leak"}}, nil
}

func TestURLQueueTransportV1ControlsResolveLazilyAndNeverProjectMediaURL(t *testing.T) {
	items := []api.Item{
		{Source: api.SourceAudius, Kind: api.KindSong, ID: "audius:song:1", ProviderID: "1", Ref: "audius:song:1", URL: "https://audius.co/u/one", Title: "One"},
		{Source: api.SourceAudius, Kind: api.KindSong, ID: "audius:song:2", ProviderID: "2", Ref: "audius:song:2", URL: "https://audius.co/u/two", Title: "Two"},
		{Source: api.SourceAudius, Kind: api.KindSong, ID: "audius:song:3", ProviderID: "3", Ref: "audius:song:3", URL: "https://audius.co/u/three", Title: "Three"},
	}
	var resolved []string
	plan := NewURLQueuePlan(api.SourceAudius, items, 1, func(_ context.Context, item api.Item) (urlResolution, error) {
		resolved = append(resolved, item.ProviderID)
		return urlResolution{URL: "https://signed.invalid/" + item.ProviderID, Duration: 90}, nil
	})
	driver := &fakeURLDriver{}
	transport := NewURLQueueTransport(driver)

	started, err := transport.Start(context.Background(), plan, 7, "session-7")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(resolved) != "[2]" || len(driver.targets) != 1 {
		t.Fatalf("initial resolutions=%v targets=%d", resolved, len(driver.targets))
	}
	assertNoSignedURL(t, started)
	if target := driver.targets[0]; target.PlaybackGeneration != 7 || target.TransportSessionID != "session-7" || target.Item.ID != "2" {
		t.Fatalf("target=%+v", target)
	}
	listed := transport.List()
	if listed.Source == nil || *listed.Source != api.SourceAudius || listed.Index != 1 || listed.QueueRevision != 1 || len(listed.Items) != 3 {
		t.Fatalf("list=%+v", listed)
	}
	for _, item := range listed.Items {
		if strings.Contains(item.URL, "signed.invalid") {
			t.Fatalf("signed URL in list: %+v", item)
		}
	}

	paused, err := transport.Pause(context.Background())
	if err != nil || paused.Status != "paused" || driver.pauses != 1 {
		t.Fatalf("pause state=%+v err=%v", paused, err)
	}
	resumed, err := transport.Resume(context.Background())
	if err != nil || resumed.Status != "playing" || driver.resumes != 1 {
		t.Fatalf("resume state=%+v err=%v", resumed, err)
	}
	if _, err := transport.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Previous(context.Background()); err != nil {
		t.Fatal(err)
	}
	jumped, err := transport.Jump(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSignedURL(t, jumped)
	if fmt.Sprint(resolved) != "[2 3 2 1]" {
		t.Fatalf("resolutions=%v", resolved)
	}
	state, err := transport.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertNoSignedURL(t, state)

	stopped, err := transport.Stop(context.Background())
	if err != nil || stopped.Status != "stopped" || driver.stops != 1 {
		t.Fatalf("stop state=%+v err=%v", stopped, err)
	}
	listed = transport.List()
	if listed.Source != nil || listed.Index != -1 || len(listed.Items) != 0 || listed.QueueRevision != 2 {
		t.Fatalf("stopped list=%+v", listed)
	}
}

func TestURLQueueTransportRetriesOnceOnMediaFailure(t *testing.T) {
	driver := &flakyURLDriver{}
	transport := NewURLQueueTransport(driver)
	item := api.Item{Source: api.SourceAudius, Kind: api.KindSong, ProviderID: "1", Title: "One"}
	resolutions := 0
	plan := NewURLQueuePlan(api.SourceAudius, []api.Item{item}, 0, func(context.Context, api.Item) (urlResolution, error) {
		resolutions++
		return urlResolution{URL: "https://signed.invalid/1", Duration: 30}, nil
	})
	if _, err := transport.Start(context.Background(), plan, 1, "session"); err != nil {
		t.Fatalf("start: %v", err)
	}
	// First media failure re-resolves and replays once.
	if _, err := transport.RetryCurrent(context.Background()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if resolutions != 2 || driver.plays != 2 {
		t.Fatalf("resolutions=%d plays=%d, want 2/2", resolutions, driver.plays)
	}
	// A second failure ends the session and clears the queue.
	driver.failures = 1
	if _, err := transport.RetryCurrent(context.Background()); err == nil {
		t.Fatal("second retry succeeded")
	}
	if queue := transport.List(); queue.Source != nil || len(queue.Items) != 0 {
		t.Fatalf("queue after second failure = %+v", queue)
	}
}

func TestURLQueueTransportEditsPreserveCurrentAndPaused(t *testing.T) {
	items := []api.Item{
		{Source: api.SourceAudius, Kind: api.KindSong, ProviderID: "1", ID: "audius:song:1", Title: "One"},
		{Source: api.SourceAudius, Kind: api.KindSong, ProviderID: "2", ID: "audius:song:2", Title: "Two"},
		{Source: api.SourceAudius, Kind: api.KindSong, ProviderID: "3", ID: "audius:song:3", Title: "Three"},
	}
	driver := &flakyURLDriver{}
	transport := NewURLQueueTransport(driver)
	plan := NewURLQueuePlan(api.SourceAudius, items, 1, func(context.Context, api.Item) (urlResolution, error) {
		return urlResolution{URL: "https://signed.invalid/x", Duration: 10}, nil
	})
	if _, err := transport.Start(context.Background(), plan, 1, "s"); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Pause(context.Background()); err != nil {
		t.Fatal(err)
	}

	added, err := transport.Add(context.Background(), []api.Item{{Source: api.SourceAudius, Kind: api.KindSong, ProviderID: "4", ID: "audius:song:4", Title: "Four"}}, "next")
	if err != nil || added.Status != "paused" || added.QueueIndex != 1 {
		t.Fatalf("after add: %+v err=%v", added, err)
	}
	removed, err := transport.Remove(context.Background(), 0)
	if err != nil || removed.Status != "paused" || removed.QueueIndex != 0 || removed.Track == nil || removed.Track.ID != "2" {
		t.Fatalf("after remove-before-current: %+v err=%v", removed, err)
	}
	moved, err := transport.Move(context.Background(), 0, 1)
	if err != nil || moved.QueueIndex != 1 || moved.Track == nil || moved.Track.ID != "2" {
		t.Fatalf("after move: %+v err=%v", moved, err)
	}
	if _, err := transport.Remove(context.Background(), 99); !errors.Is(err, errQueueIndexOutOfRange) {
		t.Fatalf("remove bounds err=%v", err)
	}
	if _, err := transport.Move(context.Background(), 0, 99); !errors.Is(err, errQueueIndexOutOfRange) {
		t.Fatalf("move bounds err=%v", err)
	}
}

func TestURLQueueTransportMovePreservesDuplicateOccurrence(t *testing.T) {
	a1 := api.Item{Source: api.SourceAudius, Kind: api.KindSong, ProviderID: "a", ID: "audius:song:a", Title: "A1"}
	a2 := api.Item{Source: api.SourceAudius, Kind: api.KindSong, ProviderID: "a", ID: "audius:song:a", Title: "A2"}
	b := api.Item{Source: api.SourceAudius, Kind: api.KindSong, ProviderID: "b", ID: "audius:song:b", Title: "B"}
	driver := &flakyURLDriver{}
	transport := NewURLQueueTransport(driver)
	plan := NewURLQueuePlan(api.SourceAudius, []api.Item{a1, b, a2}, 2, func(context.Context, api.Item) (urlResolution, error) {
		return urlResolution{URL: "https://signed.invalid/x", Duration: 10}, nil
	})
	if _, err := transport.Start(context.Background(), plan, 1, "s"); err != nil {
		t.Fatal(err)
	}
	moved, err := transport.Move(context.Background(), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if moved.QueueIndex != 2 || moved.Track == nil || moved.Track.Title != "A2" {
		t.Fatalf("duplicate move index=%d track=%+v", moved.QueueIndex, moved.Track)
	}
}

func TestURLQueueTransportRemoveSoleItemPropagatesStopFailure(t *testing.T) {
	driver := &flakyURLDriver{}
	transport := NewURLQueueTransport(driver)
	plan := NewURLQueuePlan(api.SourceAudius, []api.Item{{Source: api.SourceAudius, Kind: api.KindSong, ProviderID: "1", ID: "audius:song:1", Title: "One"}}, 0, func(context.Context, api.Item) (urlResolution, error) {
		return urlResolution{URL: "https://signed.invalid/x", Duration: 10}, nil
	})
	if _, err := transport.Start(context.Background(), plan, 1, "s"); err != nil {
		t.Fatal(err)
	}
	driver.stopErr = fmt.Errorf("stop failed")
	if _, err := transport.Remove(context.Background(), 0); err == nil {
		t.Fatal("remove of sole item ignored stop failure")
	}
}

type flakyURLDriver struct {
	failures int
	plays    int
	stopErr  error
}

func (d *flakyURLDriver) PlayURL(_ context.Context, target core.URLPlaybackTarget) (core.PlaybackState, error) {
	d.plays++
	if d.failures > 0 {
		d.failures--
		return core.PlaybackState{}, fmt.Errorf("media URL rejected")
	}
	return core.PlaybackState{Status: "playing", Mode: "url", Track: &core.Item{URL: target.URL}}, nil
}
func (d *flakyURLDriver) PauseURL(context.Context, uint64, string) (core.PlaybackState, error) {
	return core.PlaybackState{Status: "paused", Mode: "url"}, nil
}
func (d *flakyURLDriver) ResumeURL(context.Context, uint64, string) (core.PlaybackState, error) {
	return core.PlaybackState{Status: "playing", Mode: "url"}, nil
}
func (d *flakyURLDriver) StopURL(context.Context, uint64, string) (core.PlaybackState, error) {
	if d.stopErr != nil {
		return core.PlaybackState{}, d.stopErr
	}
	return core.PlaybackState{Status: "stopped", Mode: "url"}, nil
}
func (d *flakyURLDriver) StateURL(context.Context, uint64, string) (core.PlaybackState, error) {
	return core.PlaybackState{Status: "playing", Mode: "url"}, nil
}

func assertNoSignedURL(t *testing.T, state core.PlaybackState) {
	t.Helper()
	if state.Track != nil && strings.Contains(state.Track.URL, "signed.invalid") {
		t.Fatalf("signed URL in track: %+v", state.Track)
	}
	for _, item := range state.Queue {
		if strings.Contains(item.URL, "signed.invalid") {
			t.Fatalf("signed URL in queue: %+v", item)
		}
	}
}

func TestURLQueueTransportFailedStartClearsQueue(t *testing.T) {
	driver := &fakeURLDriver{}
	transport := NewURLQueueTransport(driver)
	item := api.Item{Source: api.SourceAudius, Kind: api.KindSong, ProviderID: "1", Title: "One"}
	plan := NewURLQueuePlan(api.SourceAudius, []api.Item{item}, 0, func(context.Context, api.Item) (urlResolution, error) {
		return urlResolution{}, fmt.Errorf("resolve failed")
	})
	if _, err := transport.Start(context.Background(), plan, 1, "session"); err == nil {
		t.Fatal("Start succeeded")
	}
	if queue := transport.List(); queue.Source != nil || len(queue.Items) != 0 || queue.Index != -1 {
		t.Fatalf("queue after failure=%+v", queue)
	}
}
