package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
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

type verifyingURLDriver struct {
	fakeURLDriver
	media core.PlaybackState
}

func (d *verifyingURLDriver) PlayURL(ctx context.Context, target URLPlaybackTarget) (core.PlaybackState, error) {
	d.fakeURLDriver.PlayURL(ctx, target)
	return core.PlaybackState{Status: "buffering"}, nil
}

func (d *verifyingURLDriver) StateURL(context.Context, uint64, string) (core.PlaybackState, error) {
	return d.media, nil
}

func TestAppleURLQueueVerifiesActualMediaLengthPerItem(t *testing.T) {
	items := []api.Item{
		{Source: api.SourceAppleMusic, Kind: api.KindSong, ProviderID: "one", ID: "am:one", Ref: "apple-music:song:one", URL: "https://music.apple.com/cn/song/one"},
		{Source: api.SourceAppleMusic, Kind: api.KindSong, ProviderID: "two", ID: "am:two", Ref: "apple-music:song:two", URL: "https://music.apple.com/cn/song/two"},
	}
	plan := NewURLQueuePlanWithMode(api.SourceAppleMusic, items, 0, URLQueueUnverified,
		func(_ context.Context, item api.Item) (urlResolution, error) {
			return urlResolution{URL: item.URL, Mode: URLQueueUnverified, Duration: 204}, nil
		})
	driver := &verifyingURLDriver{}
	transport := NewURLQueueTransport(driver)
	initial, err := transport.Start(context.Background(), plan, 1, "session")
	if err != nil || initial.Mode != "unverified" || driver.targets[0].Duration != 204 {
		t.Fatalf("initial=%+v target=%+v err=%v", initial, driver.targets, err)
	}
	driver.media = core.PlaybackState{Status: "playing", Duration: 90}
	short, err := transport.State(context.Background())
	if err != nil || short.Mode != "preview" {
		t.Fatalf("short media=%+v err=%v, want preview", short, err)
	}
	full := transport.Snapshot(core.PlaybackState{Status: "playing", Duration: 204})
	if full.Mode != "full" {
		t.Fatalf("full media=%+v, want full", full)
	}
	advanced, err := transport.Next(context.Background())
	if err != nil || advanced.Mode != "unverified" {
		t.Fatalf("next item=%+v err=%v, must not inherit previous mode", advanced, err)
	}
}

func TestAppleURLQueueWithoutCatalogLengthNeverClaimsFull(t *testing.T) {
	item := api.Item{Source: api.SourceAppleMusic, Kind: api.KindSong, ProviderID: "one", ID: "am:one", Ref: "apple-music:song:one", URL: "https://music.apple.com/cn/song/one"}
	plan := NewURLQueuePlanWithMode(api.SourceAppleMusic, []api.Item{item}, 0, URLQueueUnverified,
		func(context.Context, api.Item) (urlResolution, error) {
			return urlResolution{URL: item.URL, Mode: URLQueueUnverified}, nil
		})
	transport := NewURLQueueTransport(&verifyingURLDriver{})
	if _, err := transport.Start(context.Background(), plan, 1, "session"); err != nil {
		t.Fatal(err)
	}
	if state := transport.Snapshot(core.PlaybackState{Status: "playing", Duration: 204}); state.Mode != "unverified" {
		t.Fatalf("unknown catalog length reported as %q", state.Mode)
	}
}

func TestAppleShortCatalogTrackDoesNotPassAnOversizedTolerance(t *testing.T) {
	item := api.Item{Source: api.SourceAppleMusic, Kind: api.KindSong, ProviderID: "one", ID: "am:one", Ref: "apple-music:song:one", URL: "https://music.apple.com/cn/song/one"}
	plan := NewURLQueuePlanWithMode(api.SourceAppleMusic, []api.Item{item}, 0, URLQueueUnverified,
		func(context.Context, api.Item) (urlResolution, error) {
			return urlResolution{URL: item.URL, Mode: URLQueueUnverified, Duration: 8}, nil
		})
	transport := NewURLQueueTransport(&verifyingURLDriver{})
	if _, err := transport.Start(context.Background(), plan, 1, "session"); err != nil {
		t.Fatal(err)
	}
	if state := transport.Snapshot(core.PlaybackState{Status: "playing", Duration: 2}); state.Mode != "preview" {
		t.Fatalf("2-second media against 8-second song reported %q", state.Mode)
	}
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
	// A second failure ends the session and clears the queue. The sentinel
	// error tells the supervisor this was a stall, not an upstream failure.
	driver.failures = 1
	_, err := transport.RetryCurrent(context.Background())
	if err == nil || !errors.Is(err, errURLRetryExhausted) {
		t.Fatalf("second retry = %v, want the retry-exhausted sentinel", err)
	}
	if queue := transport.List(); queue.Source != nil || len(queue.Items) != 0 {
		t.Fatalf("queue after second failure = %+v", queue)
	}
}

// urlSkipPlan builds a multi-item plan whose resolver records the provider id
// of every resolution, so the skip tests can follow exactly which item played.
func urlSkipPlan(items []api.Item, startIndex int, resolved *[]string) URLQueuePlan {
	return NewURLQueuePlan(api.SourceAudius, items, startIndex, func(_ context.Context, item api.Item) (urlResolution, error) {
		*resolved = append(*resolved, item.ProviderID)
		return urlResolution{URL: "https://signed.invalid/" + item.ProviderID, Duration: 90}, nil
	})
}

func urlSkipItems(ids ...string) []api.Item {
	items := make([]api.Item, 0, len(ids))
	for _, id := range ids {
		items = append(items, api.Item{Source: api.SourceAudius, Kind: api.KindSong, ProviderID: id, ID: "audius:song:" + id, Title: "Track " + id})
	}
	return items
}

// A dead item mid-queue is skipped, not fatal: the exhausted retry budget
// advances to the next item, the skipped item is named in the sentinel, the
// new item gets its own fresh retry budget, and only the last item's
// exhaustion ends the session.
func TestURLQueueTransportSkipsDeadItemMidQueue(t *testing.T) {
	var resolved []string
	transport := NewURLQueueTransport(&flakyURLDriver{})
	if _, err := transport.Start(context.Background(), urlSkipPlan(urlSkipItems("1", "2", "3"), 0, &resolved), 1, "session"); err != nil {
		t.Fatal(err)
	}
	// First media failure on "1": one re-resolve of the same item.
	if _, err := transport.RetryCurrent(context.Background()); err != nil {
		t.Fatalf("retry one: %v", err)
	}
	// The re-resolved stream stays dead: skip to "2" instead of ending.
	state, err := transport.RetryCurrent(context.Background())
	if !errors.Is(err, errDeadItemSkipped) {
		t.Fatalf("second retry = %v, want the skip sentinel", err)
	}
	if state.Track == nil || state.Track.ID != "2" || state.QueueIndex != 1 {
		t.Fatalf("skipped state = %+v", state)
	}
	if !strings.Contains(err.Error(), `"Track 1"`) {
		t.Fatalf("skip error does not name the dead item: %v", err)
	}
	if queue := transport.List(); queue.Index != 1 || len(queue.Items) != 3 {
		t.Fatalf("queue after skip = %+v", queue)
	}
	// The skipped-to item has its own budget: one more re-resolve of "2"
	// succeeds, then its exhaustion skips to "3".
	if _, err := transport.RetryCurrent(context.Background()); err != nil {
		t.Fatalf("retry on skipped item: %v", err)
	}
	if state, err := transport.RetryCurrent(context.Background()); !errors.Is(err, errDeadItemSkipped) || state.Track == nil || state.Track.ID != "3" {
		t.Fatalf("skip of second dead item = %+v, %v", state, err)
	}
	// "3" is the last item: its exhaustion ends the session as before.
	if _, err := transport.RetryCurrent(context.Background()); err != nil {
		t.Fatalf("retry on last item: %v", err)
	}
	if _, err := transport.RetryCurrent(context.Background()); !errors.Is(err, errURLRetryExhausted) {
		t.Fatalf("last-item exhaustion = %v, want the retry-exhausted sentinel", err)
	}
	if queue := transport.List(); queue.Source != nil || len(queue.Items) != 0 {
		t.Fatalf("queue after final exhaustion = %+v", queue)
	}
	if fmt.Sprint(resolved) != "[1 1 2 2 3 3]" {
		t.Fatalf("resolutions = %v", resolved)
	}
}

// Two consecutive dead skips are all the queue spends on a dead run: a third
// consecutive dead item ends the session even though more items remain.
func TestURLQueueTransportSkipBudgetEndsSessionAfterConsecutiveDeadItems(t *testing.T) {
	var resolved []string
	transport := NewURLQueueTransport(&flakyURLDriver{})
	if _, err := transport.Start(context.Background(), urlSkipPlan(urlSkipItems("1", "2", "3", "4"), 0, &resolved), 1, "session"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := transport.RetryCurrent(context.Background()); err != nil {
			t.Fatalf("recover item %d: %v", i+1, err)
		}
		if _, err := transport.RetryCurrent(context.Background()); !errors.Is(err, errDeadItemSkipped) {
			t.Fatalf("skip item %d = %v, want the skip sentinel", i+1, err)
		}
	}
	// Item "3" burns its own retry budget, then the spent skip budget ends
	// the session: "4" is never resolved.
	if _, err := transport.RetryCurrent(context.Background()); err != nil {
		t.Fatalf("recover item 3: %v", err)
	}
	if _, err := transport.RetryCurrent(context.Background()); !errors.Is(err, errURLRetryExhausted) {
		t.Fatalf("third consecutive dead item = %v, want the retry-exhausted sentinel", err)
	}
	if queue := transport.List(); queue.Source != nil || len(queue.Items) != 0 {
		t.Fatalf("queue after spent skip budget = %+v", queue)
	}
	if fmt.Sprint(resolved) != "[1 1 2 2 3 3]" {
		t.Fatalf("resolutions = %v, item 4 must never play", resolved)
	}
}

// A user-driven item transition restarts the skip budget: jumping away from a
// dead run means the next dead item is skip-eligible again.
func TestURLQueueTransportUserJumpResetsSkipBudget(t *testing.T) {
	var resolved []string
	transport := NewURLQueueTransport(&flakyURLDriver{})
	if _, err := transport.Start(context.Background(), urlSkipPlan(urlSkipItems("1", "2", "3", "4", "5"), 0, &resolved), 1, "session"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := transport.RetryCurrent(context.Background()); err != nil {
			t.Fatalf("recover item %d: %v", i+1, err)
		}
		if _, err := transport.RetryCurrent(context.Background()); !errors.Is(err, errDeadItemSkipped) {
			t.Fatalf("skip item %d = %v, want the skip sentinel", i+1, err)
		}
	}
	if _, err := transport.Jump(context.Background(), 3); err != nil {
		t.Fatalf("user jump: %v", err)
	}
	// Without the reset the spent budget would end the session here; with it
	// the dead "4" is skipped to "5".
	if _, err := transport.RetryCurrent(context.Background()); err != nil {
		t.Fatalf("recover item 4: %v", err)
	}
	state, err := transport.RetryCurrent(context.Background())
	if !errors.Is(err, errDeadItemSkipped) || state.Track == nil || state.Track.ID != "5" {
		t.Fatalf("skip after user jump = %+v, %v", state, err)
	}
	if fmt.Sprint(resolved) != "[1 1 2 2 3 4 4 5]" {
		t.Fatalf("resolutions = %v", resolved)
	}
}

// A skip target that fails to resolve is systemic, not one dead item: the
// session ends carrying the real upstream error.
func TestURLQueueTransportSkipTargetFailureEndsSession(t *testing.T) {
	resolveErr := errors.New("upstream rejected the resolve")
	plan := NewURLQueuePlan(api.SourceAudius, urlSkipItems("1", "2"), 0, func(_ context.Context, item api.Item) (urlResolution, error) {
		if item.ProviderID == "2" {
			return urlResolution{}, resolveErr
		}
		return urlResolution{URL: "https://signed.invalid/1", Duration: 90}, nil
	})
	driver := &fakeURLDriver{}
	transport := NewURLQueueTransport(driver)
	if _, err := transport.Start(context.Background(), plan, 1, "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.RetryCurrent(context.Background()); err != nil {
		t.Fatalf("retry one: %v", err)
	}
	_, err := transport.RetryCurrent(context.Background())
	if !errors.Is(err, resolveErr) {
		t.Fatalf("skip target failure = %v, want the upstream resolve error", err)
	}
	if errors.Is(err, errDeadItemSkipped) || errors.Is(err, errURLRetryExhausted) {
		t.Fatalf("skip target failure = %v, must not be masked by a sentinel", err)
	}
	if queue := transport.List(); queue.Source != nil || len(queue.Items) != 0 {
		t.Fatalf("queue after skip target failure = %+v", queue)
	}
	// The queue owns logical invalidation; server terminal handling owns the
	// independent cleanup budget and retained backend identity.
	if driver.stops != 0 {
		t.Fatalf("stops = %d, transport must not spend execution context on cleanup", driver.stops)
	}
	if err := transport.cleanup(context.Background()); err != nil || driver.stops != 1 {
		t.Fatalf("cleanup = %v, stops = %d", err, driver.stops)
	}
}

// Skipping while paused keeps the queue paused: the new item starts and is
// paused right back, like a user-driven next.
func TestURLQueueTransportSkipPreservesPaused(t *testing.T) {
	var resolved []string
	driver := &fakeURLDriver{}
	transport := NewURLQueueTransport(driver)
	if _, err := transport.Start(context.Background(), urlSkipPlan(urlSkipItems("1", "2"), 0, &resolved), 1, "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Pause(context.Background()); err != nil {
		t.Fatal(err)
	}
	retried, err := transport.RetryCurrent(context.Background())
	if err != nil || retried.Status != "paused" {
		t.Fatalf("retry while paused = %+v, %v, want paused", retried, err)
	}
	state, err := transport.RetryCurrent(context.Background())
	if !errors.Is(err, errDeadItemSkipped) {
		t.Fatalf("skip while paused = %v, want the skip sentinel", err)
	}
	if state.Status != "paused" || state.Track == nil || state.Track.ID != "2" {
		t.Fatalf("skipped paused state = %+v", state)
	}
	if driver.pauses != 3 {
		t.Fatalf("pauses = %d, want the user pause plus retry and skip re-pauses", driver.pauses)
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
	removedOutcome, _, err := transport.Remove(context.Background(), 0)
	removed := removedOutcome.State
	if err != nil || removed.Status != "paused" || removed.QueueIndex != 0 || removed.Track == nil || removed.Track.ID != "2" {
		t.Fatalf("after remove-before-current: %+v err=%v", removed, err)
	}
	moved, err := transport.Move(context.Background(), 0, 1)
	if err != nil || moved.QueueIndex != 1 || moved.Track == nil || moved.Track.ID != "2" {
		t.Fatalf("after move: %+v err=%v", moved, err)
	}
	if _, _, err := transport.Remove(context.Background(), 99); !errors.Is(err, errQueueIndexOutOfRange) {
		t.Fatalf("remove bounds err=%v", err)
	}
	if _, err := transport.Move(context.Background(), 0, 99); !errors.Is(err, errQueueIndexOutOfRange) {
		t.Fatalf("move bounds err=%v", err)
	}
}

// advancingURLDriver is a driver whose live position advances on every state
// sample, so an answer projected from a snapshot cached at start or pause time
// is observably stale against it.
type advancingURLDriver struct {
	status   string
	position float64
}

func (d *advancingURLDriver) PlayURL(_ context.Context, _ URLPlaybackTarget) (core.PlaybackState, error) {
	d.status = "playing"
	d.position = 0
	return core.PlaybackState{Status: "playing", Mode: "url", Position: 0, Duration: 600}, nil
}
func (d *advancingURLDriver) PauseURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.status = "paused"
	return core.PlaybackState{Status: "paused", Mode: "url"}, nil
}
func (d *advancingURLDriver) ResumeURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.status = "playing"
	return core.PlaybackState{Status: "playing", Mode: "url"}, nil
}
func (d *advancingURLDriver) StopURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.status = "stopped"
	return core.PlaybackState{Status: "stopped", Mode: "url"}, nil
}
func (d *advancingURLDriver) StateURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.position += 7
	return core.PlaybackState{Status: d.status, Mode: "url", Position: d.position, Duration: 600}, nil
}

// TestURLQueueTransportEditsAnswerWithLiveProjection is the family-level pin
// for batch 2026-10-03-full f15: enqueueing with e/E (queue.add next/append)
// and every other URL-queue edit used to be answered from the state cached at
// the last start or pause, so NOW PLAYING's progress rewound right after the
// edit and only recovered on the next poll. Every edit answer must instead be
// the live projection State() would return: while the same track keeps
// playing, its position never moves backwards, and its Track/QueueIndex agree
// with the projection State() returns right after. A jump restarts the target
// track, so for it the fresh restart — not backwards continuity — is pinned
// against the follow-up state.
func TestURLQueueTransportEditsAnswerWithLiveProjection(t *testing.T) {
	driver := &advancingURLDriver{}
	transport := NewURLQueueTransport(driver)
	plan := NewURLQueuePlan(api.SourceAudius, urlSkipItems("1", "2", "3"), 0, func(context.Context, api.Item) (urlResolution, error) {
		return urlResolution{URL: "https://signed.invalid/x", Duration: 600}, nil
	})
	started, err := transport.Start(context.Background(), plan, 1, "session")
	if err != nil {
		t.Fatal(err)
	}
	previous := started.Position

	// assertEdit checks one same-track edit answer and records its position as
	// the continuity baseline for the next one.
	assertEdit := func(label string, edited core.PlaybackState, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if edited.Track == nil || edited.Track.ID != "1" || edited.QueueIndex != 0 {
			t.Fatalf("%s disturbed the current track: %+v", label, edited)
		}
		if edited.Position < previous {
			t.Fatalf("%s rewound the position from %v to %v (f15 e/E regression)", label, previous, edited.Position)
		}
		previous = edited.Position
		after, stateErr := transport.State(context.Background())
		if stateErr != nil {
			t.Fatalf("%s follow-up state: %v", label, stateErr)
		}
		if after.Track == nil || after.Track.ID != edited.Track.ID || after.QueueIndex != edited.QueueIndex {
			t.Fatalf("%s answer %+v disagrees with the following state %+v", label, edited, after)
		}
		if after.Position < edited.Position {
			t.Fatalf("%s state rewound behind its own edit: %v then %v", label, edited.Position, after.Position)
		}
		previous = after.Position
	}

	playing, err := transport.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if playing.Position <= started.Position {
		t.Fatalf("the driver did not advance: start=%v state=%v", started.Position, playing.Position)
	}
	previous = playing.Position

	// e: enqueue the selected row as the next track.
	added, err := transport.Add(context.Background(), urlSkipItems("4"), "next")
	assertEdit("add next", added, err)
	// E: append at the tail.
	appended, err := transport.Add(context.Background(), urlSkipItems("5"), "append")
	assertEdit("add append", appended, err)
	// Reorder two future rows; the current track must not notice.
	moved, err := transport.Move(context.Background(), 3, 2)
	assertEdit("move", moved, err)
	// Drop a future row, then put it back through the undo receipt — an
	// insertion like any other edit, answered the same live way.
	removed, undo, err := transport.Remove(context.Background(), 2)
	assertEdit("remove future", removed.State, err)
	if undo == nil {
		t.Fatal("removing a future song offered no undo receipt")
	}
	restored, err := transport.RestoreRemoved(context.Background(), *undo)
	assertEdit("restore removed", restored, err)

	// A jump restarts the target item: position continuity does not apply, but
	// the answer and the following state must still describe the same restart.
	jumped, err := transport.Jump(context.Background(), 1)
	if err != nil {
		t.Fatalf("jump: %v", err)
	}
	if jumped.Track == nil || jumped.Track.ID != "4" || jumped.QueueIndex != 1 {
		t.Fatalf("jump landed wrong: %+v", jumped)
	}
	after, err := transport.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Track == nil || after.Track.ID != jumped.Track.ID || after.QueueIndex != jumped.QueueIndex {
		t.Fatalf("jump answer %+v disagrees with the following state %+v", jumped, after)
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
	if _, _, err := transport.Remove(context.Background(), 0); err == nil {
		t.Fatal("remove of sole item ignored stop failure")
	}
}

type flakyURLDriver struct {
	failures int
	plays    int
	stopErr  error
	// status models what the real helper reports from StateURL: the last
	// transition the driver actually applied, not a constant.
	status string
}

func (d *flakyURLDriver) PlayURL(_ context.Context, target core.URLPlaybackTarget) (core.PlaybackState, error) {
	d.plays++
	if d.failures > 0 {
		d.failures--
		return core.PlaybackState{}, fmt.Errorf("media URL rejected")
	}
	d.status = "playing"
	return core.PlaybackState{Status: "playing", Mode: "url", Track: &core.Item{URL: target.URL}}, nil
}
func (d *flakyURLDriver) PauseURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.status = "paused"
	return core.PlaybackState{Status: "paused", Mode: "url"}, nil
}
func (d *flakyURLDriver) ResumeURL(context.Context, uint64, string) (core.PlaybackState, error) {
	d.status = "playing"
	return core.PlaybackState{Status: "playing", Mode: "url"}, nil
}
func (d *flakyURLDriver) StopURL(context.Context, uint64, string) (core.PlaybackState, error) {
	if d.stopErr != nil {
		return core.PlaybackState{}, d.stopErr
	}
	d.status = "stopped"
	return core.PlaybackState{Status: "stopped", Mode: "url"}, nil
}
func (d *flakyURLDriver) StateURL(context.Context, uint64, string) (core.PlaybackState, error) {
	return core.PlaybackState{Status: d.status, Mode: "url"}, nil
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

// A preview queue must report the public mode the client acts on: a 30-second
// excerpt presented as "full" would be a lie the UI and the skill both read.
func TestURLQueueTransportReportsThePlanMode(t *testing.T) {
	items := []api.Item{
		{Source: api.SourceAppleMusic, Kind: api.KindSong, ID: "am:1", ProviderID: "1", Ref: "am:1", PreviewURL: "https://example.invalid/p.m4a", Title: "One"},
	}
	plan := NewURLQueuePlanWithMode(api.SourceAppleMusic, items, 0, URLQueuePreview, func(context.Context, api.Item) (urlResolution, error) {
		return urlResolution{URL: "https://example.invalid/p.m4a", Duration: 30}, nil
	})
	transport := NewURLQueueTransport(&fakeURLDriver{})

	started, err := transport.Start(context.Background(), plan, 3, "session-3")
	if err != nil {
		t.Fatal(err)
	}
	if started.Mode != "preview" {
		t.Fatalf("mode = %q, want preview", started.Mode)
	}
	// Every later projection of the same session keeps the mode.
	state, err := transport.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Mode != "preview" {
		t.Fatalf("state mode = %q, want preview", state.Mode)
	}
}

// A full-length queue keeps reporting "full"; the default must not drift.
func TestURLQueueTransportKeepsFullModeForDirectURLSources(t *testing.T) {
	items := []api.Item{
		{Source: api.SourceAudius, Kind: api.KindSong, ID: "audius:song:1", ProviderID: "1", Ref: "audius:song:1", Title: "One"},
	}
	plan := NewURLQueuePlan(api.SourceAudius, items, 0, func(context.Context, api.Item) (urlResolution, error) {
		return urlResolution{URL: "https://signed.invalid/1", Duration: 90}, nil
	})
	transport := NewURLQueueTransport(&fakeURLDriver{})
	started, err := transport.Start(context.Background(), plan, 1, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if started.Mode != "full" {
		t.Fatalf("mode = %q, want full", started.Mode)
	}
}

// A plan that declares no supported mode must fail loudly instead of silently
// defaulting to "full".
func TestURLQueueTransportRejectsAPlanWithoutAMode(t *testing.T) {
	items := []api.Item{
		{Source: api.SourceAppleMusic, Kind: api.KindSong, ID: "am:1", ProviderID: "1", Ref: "am:1", Title: "One"},
	}
	plan := URLQueuePlan{source: api.SourceAppleMusic, queue: items, startIndex: 0, resolve: func(context.Context, api.Item) (urlResolution, error) {
		return urlResolution{URL: "https://example.invalid/p.m4a"}, nil
	}}
	transport := NewURLQueueTransport(&fakeURLDriver{})
	if _, err := transport.Start(context.Background(), plan, 1, "session-1"); err == nil {
		t.Fatal("a plan without a declared mode must not start")
	}
}
