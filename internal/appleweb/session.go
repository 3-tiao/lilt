package appleweb

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SignInBudget is the sign-in window the Apple auth provider declares to the
// server. The budget has one owner: the provider declares it, the server
// builds the flow's context from it, and the sign-in loop below simply runs
// until that context ends.
const SignInBudget = 10 * time.Minute

// Engine is the lazily started browser session shared by Apple discovery and
// Apple playback. Starting Chromium costs seconds and hundreds of megabytes, so
// it happens on the first Apple operation and the session then stays up; an
// idle policy can be layered on later without changing callers.
type Engine struct {
	mu        sync.Mutex
	lifecycle sync.Mutex
	options   Options
	browser   *Browser
	signingIn bool
	// widevine is the EME answer cached from the current browser start. It is
	// refreshed every start, because the binary on disk can change between
	// starts; it is deliberately not cleared on close, since it describes the
	// browser that was there, not a claim about the next one.
	widevine WidevineProbe
}

// NewEngine returns an engine that starts its browser on first use.
func NewEngine(options Options) *Engine {
	return &Engine{options: options}
}

// session returns the live browser, starting it if needed.
func (e *Engine) session(ctx context.Context) (*Browser, error) {
	e.mu.Lock()
	if e.signingIn {
		e.mu.Unlock()
		return nil, ErrSignInInProgress
	}
	if e.browser != nil {
		browser := e.browser
		if !browser.Dead() {
			e.mu.Unlock()
			return browser, nil
		}
	}
	e.mu.Unlock()

	// Start, replacement, interactive sign-in, close, and disconnect all pass
	// through one lifecycle lock. In particular there is never more than one
	// Chromium launched by this Engine for its profile.
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	e.mu.Lock()
	if e.signingIn {
		e.mu.Unlock()
		return nil, ErrSignInInProgress
	}
	if e.browser != nil {
		browser := e.browser
		if !browser.Dead() {
			e.mu.Unlock()
			return browser, nil
		}
		e.browser = nil
		e.mu.Unlock()
		_ = browser.Close()
	} else {
		e.mu.Unlock()
	}

	browser, err := Start(ctx, e.options)
	if err != nil {
		return nil, err
	}
	if err := browser.WaitMusicKit(ctx); err != nil {
		_ = browser.Close()
		return nil, err
	}
	// MusicKit is up: move the page onto the account's own storefront before
	// anything else, so the probe and every catalog call below see the region
	// playback rights are actually granted in. A session without a usable
	// account keeps its page, and a failed alignment keeps the session.
	browser.alignStorefront(ctx, e.options.Log)
	// The page is up and already evaluated: ask it about Widevine in the same
	// start, so the capability answer costs no extra browser and follows the
	// binary that is actually running.
	probe := browser.probeWidevine(ctx)
	e.mu.Lock()
	e.widevine = probe
	e.browser = browser
	e.mu.Unlock()
	return browser, nil
}

// HasProfile reports whether a browser profile already exists here, which means
// the engine has been used before and may hold a signed-in session. Warming up
// without one would start a browser for a source nobody has set up.
func (e *Engine) HasProfile() bool {
	if _, err := os.Stat(filepath.Join(e.options.ProfileDir, "Default")); err == nil {
		return true
	}
	return OwnsProfile(e.options.ProfileDir)
}

// WarmUp starts the session when a profile exists, so the authorization state is
// known without the user having to play something first. It is a no-op
// otherwise, and it never fails the caller: a warm-up that cannot start stays
// lazy, which is where the engine began.
func (e *Engine) WarmUp(ctx context.Context) {
	if !e.HasProfile() {
		return
	}
	_, _ = e.session(ctx)
}

// Close shuts the browser down if it is up. The engine stays usable: a rebuild of
// the playback side closes the browser, and the next catalog or playback call
// simply starts a new one, so a shared session is never left permanently dead.
func (e *Engine) Close() error {
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	return e.closeLocked()
}

func (e *Engine) closeLocked() error {
	e.mu.Lock()
	browser := e.browser
	e.browser = nil
	e.mu.Unlock()
	if browser == nil {
		return nil
	}
	return browser.Close()
}

// Started reports whether the browser is currently up. The composition uses it
// to decide whether an Apple operation would pay the cold start.
func (e *Engine) Started() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.browser != nil
}

// Widevine reports the EME probe answer cached from the last browser start. A
// browser that never started has not answered; the descriptor treats that as
// its declared precondition rather than paying a cold start to know.
func (e *Engine) Widevine() WidevineProbe {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.widevine
}

// live returns the running browser without starting one.
func (e *Engine) live() (*Browser, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.signingIn {
		return nil, ErrSignInInProgress
	}
	return e.browser, nil
}

// SignIn opens a visible browser on this profile and blocks until the user
// finishes Apple's own sign-in. It returns only once the profile is authorized.
//
// The headless session is closed first: two Chromiums cannot share one profile,
// and the sign-in window must be the one holding it. The window is closed on the
// way out — the session lives on disk, which is what --restore-last-session is
// for — so later playback stays headless.
func (e *Engine) SignIn(ctx context.Context) error {
	e.mu.Lock()
	if e.signingIn {
		e.mu.Unlock()
		return ErrSignInInProgress
	}
	e.signingIn = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.signingIn = false
		e.mu.Unlock()
	}()

	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	if err := e.closeLocked(); err != nil {
		return err
	}
	options := e.options
	options.Headless = false
	browser, err := Start(ctx, options)
	if err != nil {
		return err
	}
	defer func() { _ = browser.Close() }()
	if err := browser.WaitMusicKit(ctx); err != nil {
		return err
	}
	// No deadline of its own: the context carries the flow budget the server
	// built from SignInBudget, and adding a second head here is exactly how
	// the flow used to time out with the wrong terminal status.
	for {
		authorized, authorizedErr := browser.Authorized(ctx)
		if authorizedErr == nil && authorized {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// Disconnect closes the session and removes the profile, which is where the
// Apple session actually lives. A directory lilt did not create is left alone.
func (e *Engine) Disconnect() error {
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	_ = e.closeLocked()
	return RemoveProfile(e.options.ProfileDir)
}

// --- catalog -----------------------------------------------------------------

func (e *Engine) Authorized(ctx context.Context) (bool, error) {
	browser, err := e.session(ctx)
	if err != nil {
		return false, err
	}
	return browser.Authorized(ctx)
}

func (e *Engine) SearchSongs(ctx context.Context, term string, limit int) ([]CatalogSong, error) {
	browser, err := e.session(ctx)
	if err != nil {
		return nil, err
	}
	return browser.SearchSongs(ctx, term, limit)
}

func (e *Engine) SearchAlbums(ctx context.Context, term string, limit int) ([]CatalogAlbum, error) {
	browser, err := e.session(ctx)
	if err != nil {
		return nil, err
	}
	return browser.SearchAlbums(ctx, term, limit)
}

func (e *Engine) AlbumTracks(ctx context.Context, albumID string) (CatalogAlbum, []CatalogSong, error) {
	browser, err := e.session(ctx)
	if err != nil {
		return CatalogAlbum{}, nil, err
	}
	return browser.AlbumTracks(ctx, albumID)
}

func (e *Engine) Song(ctx context.Context, songID string) (CatalogSong, error) {
	browser, err := e.session(ctx)
	if err != nil {
		return CatalogSong{}, err
	}
	return browser.Song(ctx, songID)
}

// --- playback ----------------------------------------------------------------

func (e *Engine) PlayCatalogSong(ctx context.Context, songID string) error {
	browser, err := e.session(ctx)
	if err != nil {
		return err
	}
	return browser.PlayCatalogSong(ctx, songID)
}

func (e *Engine) Pause(ctx context.Context) error {
	// Nothing running means nothing to pause: starting a browser to pause silence
	// would cost a cold start for no reason.
	browser, err := e.live()
	if err != nil {
		return err
	}
	if browser == nil {
		return nil
	}
	return browser.Pause(ctx)
}

func (e *Engine) Resume(ctx context.Context) error {
	browser, err := e.live()
	if err != nil {
		return err
	}
	if browser == nil {
		return nil
	}
	return browser.Resume(ctx)
}

func (e *Engine) Stop(ctx context.Context) error {
	browser, err := e.live()
	if err != nil {
		return err
	}
	if browser == nil {
		return nil
	}
	return browser.Stop(ctx)
}

func (e *Engine) State(ctx context.Context) (State, error) {
	// State is read by status queries and by the progress sampler, both of which
	// must stay cheap. No browser means nothing is playing.
	browser, err := e.live()
	if err != nil {
		return State{}, err
	}
	if browser == nil {
		return State{}, nil
	}
	return browser.State(ctx)
}
