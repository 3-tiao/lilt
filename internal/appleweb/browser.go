// Package appleweb drives Apple's own MusicKit JS inside a Chromium we own.
//
// It is the browser counterpart of the signed MusicKit helper: the page provides
// Apple's developer token, the user session, and the DRM path, while this
// package only speaks the Chrome DevTools Protocol over the launching process's
// fd 3/fd 4 pipe. Nothing here needs a third-party dependency, and nothing here
// ever sees the user's Apple credentials — the page renders its own sign-in.
//
// Three properties are load-bearing and easy to get wrong, so they live in one
// place instead of at each call site:
//
//   - Every evaluation carries userGesture=true. Without it a Chromium page
//     leaves play() pending forever, with no error and no state change.
//   - The browser is launched with --restore-last-session. Apple's session
//     cookies are session cookies, so without it a browser restart is a logout.
//     That flag also restores last session's tabs, so Start closes every page
//     target except the one it drives.
//   - Shutdown goes through Browser.close, never SIGKILL: a hard kill drops the
//     profile writes that keep the session.
package appleweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ErrNoBrowser reports that no usable Chromium could be found.
var ErrNoBrowser = errors.New("no chromium with widevine support was found")

// ErrForeignProfile reports a profile directory lilt did not create, which it
// refuses to delete.
var ErrForeignProfile = errors.New("that directory is not a lilt browser profile")

// ErrSignInInProgress reports that the interactive browser owns the profile.
// Ordinary page operations must fail promptly rather than wait for the user.
var ErrSignInInProgress = errors.New("Apple Music sign-in is in progress")

// ErrProfileInUse reports that another lilt process currently owns the profile.
var ErrProfileInUse = errors.New("Apple Music browser profile is in use by another lilt server")

// ErrBrowserDead distinguishes a broken CDP transport from a page-level command
// failure. Callers may rebuild after this error, but must not replay the command
// whose outcome may be unknown.
var ErrBrowserDead = errors.New("Apple Music browser connection died")

// ErrUnauthorized reports that the browser profile is signed out, so a
// personalized request cannot be served. It is the browser counterpart of the
// helper's authorizationRequired guard; the provider maps it to the stable
// authorization_required code.
var ErrUnauthorized = errors.New("appleweb: the browser profile is signed out")

// DefaultURL is the page the engine drives first. Its region is only where a
// fresh profile lands: mk.storefrontId follows the page URL, not the signed-in
// account, so once authorization settles the engine moves the page onto the
// account's own storefront (alignStorefront).
const DefaultURL = "https://music.apple.com/us/browse"

// SignInURL is what the interactive sign-in flow points the user at.
const SignInURL = "https://music.apple.com/us/browse"

// profileMarker identifies a profile directory lilt created. Disconnecting
// removes the profile, and that must never delete a directory lilt did not own.
const profileMarker = "lilt-apple-profile"

// Options configures a browser instance.
type Options struct {
	// ProfileDir is the persistent Chromium profile. It holds the Apple
	// session, so it must survive restarts and must not be shared.
	ProfileDir string
	// URL is the page to open. Empty uses DefaultURL.
	URL string
	// ChromiumPath overrides binary discovery.
	ChromiumPath string
	// Headless runs without a window. Playback works headless, so a window is
	// only needed when the user has to sign in.
	Headless bool
	// Log receives engine events (storefront alignment) as journal entries.
	// Nil discards them.
	Log func(kind string, fields map[string]any)
	// Stderr receives Chromium's stderr. Empty discards it.
	Stderr io.Writer
}

type cdpRequest struct {
	ID        int    `json:"id"`
	Method    string `json:"method"`
	Params    any    `json:"params,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

type cdpMessage struct {
	ID        int             `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// pipe is the CDP connection. Chromium reads requests on fd 3 and writes
// replies on fd 4 as NUL-delimited JSON.
type pipe struct {
	r      *os.File
	w      *os.File
	mu     sync.Mutex
	nextID int
	wait   map[int]chan cdpMessage
	closed bool
	term   error
	once   sync.Once
	done   chan struct{}
}

func newPipe(r, w *os.File) *pipe {
	p := &pipe{r: r, w: w, wait: map[int]chan cdpMessage{}, done: make(chan struct{})}
	go p.readLoop()
	return p
}

func (p *pipe) readLoop() {
	defer p.fail(fmt.Errorf("%w: chromium closed the devtools pipe", ErrBrowserDead))
	buf := make([]byte, 0, 1<<20)
	chunk := make([]byte, 1<<16)
	for {
		n, err := p.r.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			for {
				index := -1
				for i, b := range buf {
					if b == 0 {
						index = i
						break
					}
				}
				if index < 0 {
					break
				}
				raw := buf[:index]
				buf = buf[index+1:]
				var message cdpMessage
				if json.Unmarshal(raw, &message) != nil || message.ID == 0 {
					continue
				}
				p.mu.Lock()
				waiter := p.wait[message.ID]
				delete(p.wait, message.ID)
				p.mu.Unlock()
				if waiter != nil {
					waiter <- message
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// fail makes the connection permanently unusable and releases every waiter.
func (p *pipe) fail(cause error) {
	p.once.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.term = cause
		for id, waiter := range p.wait {
			delete(p.wait, id)
			close(waiter)
		}
		p.mu.Unlock()
		_ = p.r.Close()
		_ = p.w.Close()
		close(p.done)
	})
}

func (p *pipe) close() { p.fail(errors.New("devtools pipe closed")) }

func (p *pipe) call(ctx context.Context, method string, params any, session string) (json.RawMessage, error) {
	p.mu.Lock()
	if p.closed {
		err := p.term
		p.mu.Unlock()
		return nil, err
	}
	p.nextID++
	id := p.nextID
	waiter := make(chan cdpMessage, 1)
	p.wait[id] = waiter
	p.mu.Unlock()

	payload, err := json.Marshal(cdpRequest{ID: id, Method: method, Params: params, SessionID: session})
	if err != nil {
		return nil, err
	}
	if _, err := p.w.Write(append(payload, 0)); err != nil {
		fatal := fmt.Errorf("%w: write to chromium: %v", ErrBrowserDead, err)
		p.fail(fatal)
		return nil, fatal
	}
	select {
	case message, ok := <-waiter:
		if !ok {
			return nil, p.transportError()
		}
		if message.Error != nil {
			return nil, fmt.Errorf("cdp %s: %s", method, message.Error.Message)
		}
		return message.Result, nil
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.wait, id)
		p.mu.Unlock()
		return nil, fmt.Errorf("cdp %s: %w", method, ctx.Err())
	case <-p.done:
		return nil, p.transportError()
	}
}

func (p *pipe) transportError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.term != nil {
		return p.term
	}
	return errors.New("devtools pipe is closed")
}

func (p *pipe) dead() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed && errors.Is(p.term, ErrBrowserDead)
}

// Browser owns one Chromium process and the CDP session driving its page.
type Browser struct {
	cmd      *exec.Cmd
	conn     *pipe
	session  string
	profile  string
	lockFile *os.File
	closeOne sync.Once
	closeErr error
}

// Dead reports whether Chromium's CDP transport failed unexpectedly.
func (b *Browser) Dead() bool { return b.conn.dead() }

// Start launches Chromium and attaches to the page it opened.
func Start(ctx context.Context, options Options) (*Browser, error) {
	if options.ProfileDir == "" {
		return nil, errors.New("appleweb: ProfileDir is required; it holds the Apple session")
	}
	binary, err := findChromium(options.ChromiumPath)
	if err != nil {
		return nil, err
	}
	if err := prepareProfile(options.ProfileDir); err != nil {
		return nil, err
	}
	lockFile, err := lockProfile(options.ProfileDir)
	if err != nil {
		return nil, err
	}
	releaseLock := true
	defer func() {
		if releaseLock {
			unlockProfile(lockFile)
		}
	}()
	url := options.URL
	if url == "" {
		url = DefaultURL
	}

	// fd 3: chromium reads requests. fd 4: chromium writes replies.
	toChromeR, toChromeW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	fromChromeR, fromChromeW, err := os.Pipe()
	if err != nil {
		_ = toChromeR.Close()
		_ = toChromeW.Close()
		return nil, err
	}

	stderr := options.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	cmd := exec.Command(binary, chromiumArgs(url, options.ProfileDir, options.Headless)...)
	cmd.ExtraFiles = []*os.File{toChromeR, fromChromeW}
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	// A new process group keeps the whole browser tree together, so a failed
	// start cannot leave renderers behind.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = toChromeR.Close()
		_ = toChromeW.Close()
		_ = fromChromeR.Close()
		_ = fromChromeW.Close()
		return nil, fmt.Errorf("start chromium: %w", err)
	}
	_ = toChromeR.Close()
	_ = fromChromeW.Close()

	browser := &Browser{cmd: cmd, conn: newPipe(fromChromeR, toChromeW), profile: options.ProfileDir, lockFile: lockFile}
	releaseLock = false
	if err := browser.attach(ctx, url); err != nil {
		_ = browser.Close()
		return nil, err
	}
	return browser, nil
}

// prepareProfile establishes ownership only when this call atomically creates
// the profile directory. An existing unmarked directory is intentionally left
// untouched: it may be a user's normal Chromium profile.
func prepareProfile(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return fmt.Errorf("create the browser profile parent: %w", err)
	}
	err := os.Mkdir(dir, 0o700)
	switch {
	case err == nil:
		if err := os.WriteFile(filepath.Join(dir, profileMarker), []byte("lilt\n"), 0o600); err != nil {
			_ = os.Remove(dir)
			return fmt.Errorf("mark the browser profile directory: %w", err)
		}
		return nil
	case errors.Is(err, os.ErrExist):
		info, statErr := os.Stat(dir)
		if statErr != nil {
			return fmt.Errorf("inspect the browser profile directory: %w", statErr)
		}
		if !info.IsDir() {
			return fmt.Errorf("browser profile path is not a directory: %s", dir)
		}
		return nil
	default:
		return fmt.Errorf("create the browser profile directory: %w", err)
	}
}

func lockProfile(profile string) (*os.File, error) {
	file, err := os.OpenFile(profile+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Apple Music profile lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%w: %s", ErrProfileInUse, profile)
		}
		return nil, fmt.Errorf("lock Apple Music browser profile: %w", err)
	}
	return file, nil
}

func unlockProfile(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func chromiumArgs(url, profile string, headless bool) []string {
	// These are Chromium command-line switches on both Linux and macOS. Chrome,
	// Edge, and Brave on macOS need no app-bundle-specific launch flags when
	// their Contents/MacOS executable is started directly.
	args := []string{
		"--remote-debugging-pipe",
		"--user-data-dir=" + profile,
		// Apple's session cookies are session cookies: without restore they are
		// gone on the next launch, i.e. every restart would be a logout.
		"--restore-last-session",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-features=Translate",
		"--autoplay-policy=no-user-gesture-required",
		"--window-size=1200,900",
	}
	if headless {
		args = append(args, "--headless=new")
	}
	return append(args, url)
}

// attach finds the page Chromium opened for url, takes it over, and closes every
// other page: --restore-last-session reopens last session's tabs, so without
// this each launch would leave another Apple Music tab behind. Cleanup runs once
// here, before any sign-in popup can exist, so a popup opened later is safe.
func (b *Browser) attach(ctx context.Context, url string) error {
	host := url
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	var pageID string
	var targets targetList
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) && pageID == "" {
		result, err := b.conn.call(ctx, "Target.getTargets", nil, "")
		if err != nil {
			return err
		}
		if err := json.Unmarshal(result, &targets); err != nil {
			return err
		}
		for _, target := range targets.TargetInfos {
			if target.Type == "page" && strings.Contains(target.URL, host) {
				pageID = target.TargetID
				break
			}
		}
		if pageID == "" {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	if pageID == "" {
		return fmt.Errorf("appleweb: chromium never opened a page for %s", url)
	}
	for _, target := range targets.TargetInfos {
		if target.Type == "page" && target.TargetID != pageID {
			_, _ = b.conn.call(ctx, "Target.closeTarget", map[string]any{"targetId": target.TargetID}, "")
		}
	}
	attached, err := b.conn.call(ctx, "Target.attachToTarget", map[string]any{"targetId": pageID, "flatten": true}, "")
	if err != nil {
		return err
	}
	var session struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(attached, &session); err != nil {
		return err
	}
	b.session = session.SessionID
	if _, err := b.conn.call(ctx, "Runtime.enable", nil, b.session); err != nil {
		return err
	}
	_, err = b.conn.call(ctx, "Page.enable", nil, b.session)
	return err
}

type targetList struct {
	TargetInfos []struct {
		TargetID string `json:"targetId"`
		Type     string `json:"type"`
		URL      string `json:"url"`
	} `json:"targetInfos"`
}

// Evaluate runs an expression in the page and returns its value as a string.
//
// Every evaluation carries userGesture, because the page's own play() path
// depends on user activation: without it Chromium leaves the play promise
// pending forever and reports nothing at all.
func (b *Browser) Evaluate(ctx context.Context, expression string) (string, error) {
	result, err := b.conn.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"awaitPromise":  true,
		"returnByValue": true,
		"userGesture":   true,
	}, b.session)
	if err != nil {
		return "", err
	}
	var evaluated struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(result, &evaluated); err != nil {
		return "", err
	}
	if evaluated.ExceptionDetails != nil {
		// A page exception can quote upstream resources (fetch failures embed
		// the URL it could not reach), so its text is sanitized here rather
		// than passed through to callers that surface it publicly.
		if details := evaluated.ExceptionDetails.Exception; details != nil && details.Description != "" {
			return "", fmt.Errorf("page threw: %s", sanitizeUpstreamMessage(firstLine(details.Description)))
		}
		return "", fmt.Errorf("page threw: %s", sanitizeUpstreamMessage(evaluated.ExceptionDetails.Text))
	}
	if evaluated.Result.Value == nil {
		return "", nil
	}
	return fmt.Sprint(evaluated.Result.Value), nil
}

// Ready reports whether the page finished loading.
func (b *Browser) Ready(ctx context.Context) bool {
	state, err := b.Evaluate(ctx, "document.readyState")
	return err == nil && state == "complete"
}

// Close shuts the browser down cleanly and reaps it. A hard kill would drop the
// profile writes that keep the Apple session, so Browser.close comes first and
// the kill is only a fallback.
func (b *Browser) Close() error {
	b.closeOne.Do(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, _ = b.conn.call(shutdown, "Browser.close", nil, "")
		cancel()
		b.conn.close()

		exited := make(chan struct{})
		go func() {
			_, _ = b.cmd.Process.Wait()
			close(exited)
		}()
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			_ = syscall.Kill(-b.cmd.Process.Pid, syscall.SIGKILL)
			<-exited
		}
		unlockProfile(b.lockFile)
		b.lockFile = nil
	})
	return b.closeErr
}

func firstLine(value string) string {
	if i := strings.IndexByte(value, '\n'); i >= 0 {
		return value[:i]
	}
	return value
}

// Available reports whether a usable browser exists, without starting one. The
// Apple descriptor uses it so that answering sources.list never pays a cold
// start.
func Available() error {
	_, err := findChromium("")
	return err
}

// ProfileDir reports the persistent profile this session uses.
func (e *Engine) ProfileDir() string { return e.options.ProfileDir }

// OwnsProfile reports whether the profile directory carries lilt's marker.
func OwnsProfile(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, profileMarker))
	return err == nil
}

// RemoveProfile deletes a profile lilt owns. It refuses anything else, because
// the path is user-configurable and pointing it at a real browser profile must
// not end in that profile being deleted.
func RemoveProfile(dir string) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect browser profile: %w", err)
	}
	if !OwnsProfile(dir) {
		return fmt.Errorf("%w: %s", ErrForeignProfile, dir)
	}
	return os.RemoveAll(dir)
}

// findChromium resolves the browser binary. Widevine only exists in a Chromium
// built with the CDM, so the hint names how to get one instead of pretending a
// bare chromium is enough.
func findChromium(override string) (string, error) {
	if override == "" {
		override = os.Getenv("LILT_CHROMIUM_PATH")
	}
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("%w: LILT_CHROMIUM_PATH=%s: %v; it must name a chromium built with Widevine", ErrNoBrowser, override, err)
		}
		return override, nil
	}
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		for _, candidate := range darwinChromiumCandidates(home) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	for _, name := range []string{"chromium", "google-chrome-stable", "google-chrome", "chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	if runtime.GOOS == "darwin" {
		return "", fmt.Errorf("%w; install Google Chrome, Chromium, Microsoft Edge, or Brave Browser, or set LILT_CHROMIUM_PATH", ErrNoBrowser)
	}
	return "", fmt.Errorf("%w; install Widevine chromium (nixpkgs: `chromium.override { enableWideVine = true; }`) or set LILT_CHROMIUM_PATH", ErrNoBrowser)
}

func darwinChromiumCandidates(home string) []string {
	applications := []string{"/Applications"}
	if home != "" {
		applications = append(applications, filepath.Join(home, "Applications"))
	}
	bundles := []struct{ app, binary string }{
		{"Google Chrome.app", "Google Chrome"},
		{"Chromium.app", "Chromium"},
		{"Microsoft Edge.app", "Microsoft Edge"},
		{"Brave Browser.app", "Brave Browser"},
	}
	candidates := make([]string, 0, len(applications)*len(bundles))
	for _, dir := range applications {
		for _, bundle := range bundles {
			candidates = append(candidates, filepath.Join(dir, bundle.app, "Contents", "MacOS", bundle.binary))
		}
	}
	return candidates
}

// DefaultProfileDir is where the Apple session lives: Application Support on
// macOS and XDG data on Linux, per user and machine-wide.
//
// It deliberately does NOT derive from the state root. The signed-in session is
// machine-level credential storage — one Apple login serves every lilt server on
// the machine — while a state root is per session (`LILT_STATE` isolates manual
// sessions and tests). Deriving one from the other meant a session with a fresh
// state root found no signed-in profile and silently fell back to previews.
//
// LILT_APPLE_PROFILE overrides it, which is how a profile signed in elsewhere can
// be reused (and how the opt-in E2E test points at one).
func DefaultProfileDir() string {
	if override := os.Getenv("LILT_APPLE_PROFILE"); override != "" {
		return override
	}
	if runtime.GOOS == "darwin" {
		if base, err := os.UserConfigDir(); err == nil {
			return filepath.Join(base, "lilt", "apple-browser")
		}
	}
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return filepath.Join(base, "lilt", "apple-browser")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "lilt-apple-browser")
	}
	return filepath.Join(home, ".local", "share", "lilt", "apple-browser")
}
