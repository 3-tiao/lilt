package appleweb

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// signInBudget bounds how long an interactive sign-in may stay open.
const signInBudget = 10 * time.Minute

// Engine is the lazily started browser session shared by Apple discovery and
// Apple playback. Starting Chromium costs seconds and hundreds of megabytes, so
// it happens on the first Apple operation and the session then stays up; an
// idle policy can be layered on later without changing callers.
type Engine struct {
	mu      sync.Mutex
	options Options
	browser *Browser
	// starting guards concurrent first uses: discovery runs outside the server's
	// command lock, so two Apple queries can race the first start.
	starting sync.Mutex
}

// NewEngine returns an engine that starts its browser on first use.
func NewEngine(options Options) *Engine {
	return &Engine{options: options}
}

// session returns the live browser, starting it if needed.
func (e *Engine) session(ctx context.Context) (*Browser, error) {
	e.mu.Lock()
	if e.browser != nil {
		browser := e.browser
		e.mu.Unlock()
		return browser, nil
	}
	e.mu.Unlock()

	// One starter at a time; the others wait and then reuse its browser.
	e.starting.Lock()
	defer e.starting.Unlock()
	e.mu.Lock()
	if e.browser != nil {
		browser := e.browser
		e.mu.Unlock()
		return browser, nil
	}
	e.mu.Unlock()

	browser, err := Start(ctx, e.options)
	if err != nil {
		return nil, err
	}
	if err := browser.WaitMusicKit(ctx); err != nil {
		_ = browser.Close()
		return nil, err
	}
	e.mu.Lock()
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

// live returns the running browser without starting one.
func (e *Engine) live() *Browser {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.browser
}

// SignIn opens a visible browser on this profile and blocks until the user
// finishes Apple's own sign-in. It returns only once the profile is authorized.
//
// The headless session is closed first: two Chromiums cannot share one profile,
// and the sign-in window must be the one holding it. The window is closed on the
// way out — the session lives on disk, which is what --restore-last-session is
// for — so later playback stays headless.
func (e *Engine) SignIn(ctx context.Context) error {
	if err := e.Close(); err != nil {
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
	deadline := time.Now().Add(signInBudget)
	for {
		authorized, authorizedErr := browser.Authorized(ctx)
		if authorizedErr == nil && authorized {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrSignInTimeout
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
	_ = e.Close()
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

func (e *Engine) Storefront(ctx context.Context) (string, error) {
	browser, err := e.session(ctx)
	if err != nil {
		return "", err
	}
	return browser.Storefront(ctx)
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
	browser := e.live()
	if browser == nil {
		return nil
	}
	return browser.Pause(ctx)
}

func (e *Engine) Resume(ctx context.Context) error {
	browser := e.live()
	if browser == nil {
		return nil
	}
	return browser.Resume(ctx)
}

func (e *Engine) Stop(ctx context.Context) error {
	browser := e.live()
	if browser == nil {
		return nil
	}
	return browser.Stop(ctx)
}

func (e *Engine) Next(ctx context.Context) error {
	browser, err := e.session(ctx)
	if err != nil {
		return err
	}
	return browser.Next(ctx)
}

func (e *Engine) Previous(ctx context.Context) error {
	browser, err := e.session(ctx)
	if err != nil {
		return err
	}
	return browser.Previous(ctx)
}

func (e *Engine) State(ctx context.Context) (State, error) {
	// State is read by status queries and by the progress sampler, both of which
	// must stay cheap. No browser means nothing is playing.
	browser := e.live()
	if browser == nil {
		return State{}, nil
	}
	return browser.State(ctx)
}
