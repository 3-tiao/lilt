package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/3-tiao/lilt/internal/api"
)

// fakeAuthRemote records the Account overlay's Remote calls and answers from
// canned fixtures, keeping the tests hermetic: no socket, no browser, no
// system dialog.
type fakeAuthRemote struct {
	recordingRemote

	list      []api.SourceAuthorization
	listErr   error
	listCalls int

	beginSource string
	beginFlow   api.AuthorizationFlow
	beginErr    error
	beginCalls  int

	pollFlows []api.AuthorizationFlow
	pollErr   error

	cancelCalls int
	cancelErr   error

	disconnectSource string
	disconnectCalls  int
	disconnectErr    error
}

func (r *fakeAuthRemote) AuthList(context.Context) ([]api.SourceAuthorization, error) {
	r.listCalls++
	if r.listErr != nil {
		return nil, r.listErr
	}
	return append([]api.SourceAuthorization(nil), r.list...), nil
}

func (r *fakeAuthRemote) BeginAuth(_ context.Context, source string) (api.AuthorizationFlow, error) {
	r.beginCalls++
	r.beginSource = source
	if r.beginErr != nil {
		return api.AuthorizationFlow{}, r.beginErr
	}
	return r.beginFlow, nil
}

func (r *fakeAuthRemote) FlowStatus(context.Context, string) (api.AuthorizationFlow, error) {
	if r.pollErr != nil {
		return api.AuthorizationFlow{}, r.pollErr
	}
	if len(r.pollFlows) > 0 {
		flow := r.pollFlows[0]
		r.pollFlows = r.pollFlows[1:]
		return flow, nil
	}
	return r.beginFlow, nil
}

func (r *fakeAuthRemote) CancelAuth(context.Context, string) (api.AuthorizationFlow, error) {
	r.cancelCalls++
	if r.cancelErr != nil {
		return api.AuthorizationFlow{}, r.cancelErr
	}
	return api.AuthorizationFlow{Status: api.FlowCancelled}, nil
}

func (r *fakeAuthRemote) DisconnectAuth(_ context.Context, source string) (api.SourceAuthorization, error) {
	r.disconnectCalls++
	r.disconnectSource = source
	if r.disconnectErr != nil {
		return api.SourceAuthorization{}, r.disconnectErr
	}
	return api.SourceAuthorization{Source: api.SourceID(source), Status: api.AuthNotDetermined}, nil
}

// defaultAuthList mirrors the declared source order of the fake descriptors:
// apple-music, audius, jamendo, radio.
func defaultAuthList() []api.SourceAuthorization {
	return []api.SourceAuthorization{
		{Source: api.SourceAppleMusic, Status: api.AuthNotDetermined, CanDisconnect: true},
		{Source: api.SourceAudius, Status: api.AuthAuthorized, AccountLabel: "guocai", CanDisconnect: true},
		{Source: api.SourceJamendo, Status: api.AuthNotRequired, CanDisconnect: true},
		{Source: api.SourceRadio, Status: api.AuthNotRequired, CanDisconnect: false},
	}
}

func authOverlayView(m Model) string {
	m.width, m.height = 100, 30
	return plainText(m.View().Content)
}

// openAuthOverlay opens the overlay through the palette `:auth` path and
// drains the initial authorization.list fetch, so a test starts from the
// rendered list.
func openAuthOverlay(t *testing.T, m Model) Model {
	t.Helper()
	next, cmd := m.runPaletteCommand("auth")
	m = next.(Model)
	if m.overlay != "auth" {
		t.Fatalf(":auth did not open the overlay: overlay=%q", m.overlay)
	}
	return drainAuthFetch(t, m, cmd)
}

// drainAuthFetch runs one pending authorization.list command and applies its
// message. The fetch closure performs no sleeping I/O, so it is safe to run
// synchronously (unlike the flow poll's one-second tick).
func drainAuthFetch(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected an authorization.list fetch command")
	}
	next, _ := m.Update(cmd())
	return next.(Model)
}

// pressAuthKey sends one key to the overlay and returns the model plus the
// returned command (unexecuted: begin/disconnect/cancel closures run without
// sleeping, the flow poll tick must never run in tests).
func pressAuthKey(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	next, cmd := m.handleKey(msg)
	return next.(Model), cmd
}

// beginAuthFlowFrom drives Enter on the selected row through the begin
// response, returning the poll command (which tests never execute).
func beginAuthFlowFrom(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := pressAuthKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next
	if cmd == nil {
		t.Fatal("Enter did not start authorization.begin")
	}
	message := cmd()
	begin, ok := message.(authBeginMsg)
	if !ok {
		t.Fatalf("unexpected begin message %#v", message)
	}
	updated, follow := m.Update(begin)
	return updated.(Model), follow
}

func authRow(title, status string) string {
	return fmt.Sprintf("%-12s %s", title, status)
}

func TestAuthOverlayShowsLoadingThenAllSourceStatuses(t *testing.T) {
	remote := &fakeAuthRemote{list: defaultAuthList()}
	m, _, _ := newModel(t)
	m.remote = remote
	next, cmd := m.runPaletteCommand("auth")
	m = next.(Model)
	if m.overlay != "auth" {
		t.Fatalf(":auth did not open the overlay: overlay=%q", m.overlay)
	}
	if view := authOverlayView(m); !strings.Contains(view, "loading…") {
		t.Fatalf("initial fetch does not show a loading row:\n%s", view)
	}
	m = drainAuthFetch(t, m, cmd)
	view := authOverlayView(m)
	for _, want := range []string{
		"Account",
		"› " + authRow("Apple Music", "not signed in"),
		authRow("Audius", "linked · guocai"),
		authRow("Jamendo", "not required"),
		authRow("Radio", "not required"),
		"j/k move · Enter sign in / setup · d disconnect · Esc close",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("overlay missing %q:\n%s", want, view)
		}
	}
	// The cursor starts on the browsing source's row.
	if !strings.Contains(view, "› "+authRow("Apple Music", "not signed in")) {
		t.Fatalf("cursor is not on the browsing source's row:\n%s", view)
	}
}

func TestAuthOverlayEnterBeginsAppleSystemDialogFlow(t *testing.T) {
	remote := &fakeAuthRemote{
		list: defaultAuthList(),
		beginFlow: api.AuthorizationFlow{
			FlowID: "f1", Source: api.SourceAppleMusic, Status: api.FlowPending,
			Interaction: api.Interaction{Type: api.InteractionSystemDialog},
		},
	}
	m, _, _ := newModel(t)
	m.remote = remote
	m = openAuthOverlay(t, m)

	m, follow := beginAuthFlowFrom(t, m)
	if remote.beginCalls != 1 || remote.beginSource != "apple-music" {
		t.Fatalf("begin = %d calls for %q", remote.beginCalls, remote.beginSource)
	}
	if follow == nil {
		t.Fatal("a pending flow must schedule flowStatus polling")
	}
	view := authOverlayView(m)
	for _, want := range []string{
		"Apple Music — complete the system authorization dialog…",
		"Esc cancel sign-in",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("system dialog progress missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "waiting for sign-in") {
		t.Fatalf("a system dialog flow must not render browser waiting copy:\n%s", view)
	}
}

func TestAuthOverlayEnterBeginsAppleBrowserFlowWithURL(t *testing.T) {
	remote := &fakeAuthRemote{
		list: defaultAuthList(),
		beginFlow: api.AuthorizationFlow{
			FlowID: "f2", Source: api.SourceAppleMusic, Status: api.FlowPending,
			Interaction: api.Interaction{Type: api.InteractionBrowser, URL: "https://music.apple.com/signin"},
		},
	}
	m, _, _ := newModel(t)
	m.remote = remote
	m = openAuthOverlay(t, m)

	m, _ = beginAuthFlowFrom(t, m)
	view := authOverlayView(m)
	for _, want := range []string{
		"https://music.apple.com/signin",
		"Apple Music — waiting for sign-in…",
		"ctrl+o open link · Esc cancel sign-in",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("browser flow progress missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "complete the system authorization dialog") {
		t.Fatalf("a browser flow must not render system dialog copy:\n%s", view)
	}
}

func TestAuthOverlayAudiusEnterBeginsFlowWithManualCtrlO(t *testing.T) {
	remote := &fakeAuthRemote{
		list: defaultAuthList(),
		beginFlow: api.AuthorizationFlow{
			FlowID: "f3", Source: api.SourceAudius, Status: api.FlowPending,
			Interaction: api.Interaction{Type: api.InteractionBrowser, URL: "https://audius.org/link"},
		},
	}
	m, _, _ := newModel(t)
	m.remote = remote
	opened := []string{}
	m.openURL = func(url string) { opened = append(opened, url) }
	m = openAuthOverlay(t, m)

	next, _ := pressAuthKey(m, runeKey('j'))
	m = next
	m, _ = beginAuthFlowFrom(t, m)
	if remote.beginCalls != 1 || remote.beginSource != "audius" {
		t.Fatalf("begin = %d calls for %q", remote.beginCalls, remote.beginSource)
	}
	// The URL is shown but never auto-launched: the user decides.
	if len(opened) != 0 {
		t.Fatalf("begin auto-opened a browser: %v", opened)
	}
	if view := authOverlayView(m); !strings.Contains(view, "https://audius.org/link") {
		t.Fatalf("audius flow URL missing:\n%s", view)
	}

	next, cmd := pressAuthKey(m, tea.KeyPressMsg{Code: 'o', Text: "", Mod: tea.ModCtrl})
	m = next
	if cmd == nil {
		t.Fatal("ctrl+o did not produce a launcher command")
	}
	_ = cmd()
	if len(opened) != 1 || opened[0] != "https://audius.org/link" {
		t.Fatalf("ctrl+o opened %v", opened)
	}
}

func TestAuthOverlayJamendoEnterOpensSetupModal(t *testing.T) {
	remote := &fakeAuthRemote{list: defaultAuthList()}
	m, _, _ := newModel(t)
	m.remote = remote
	m.jamendoSetup = func(context.Context, string) error { return nil }
	m = openAuthOverlay(t, m)

	next, _ := pressAuthKey(m, runeKey('j'))
	m = next
	next, _ = pressAuthKey(m, runeKey('j'))
	m = next
	next, _ = pressAuthKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next
	if m.overlay != "input" || m.inputMode != "jamendo-setup" {
		t.Fatalf("jamendo Enter must open the setup modal: overlay=%q mode=%q", m.overlay, m.inputMode)
	}
	if remote.beginCalls != 0 {
		t.Fatalf("jamendo Enter must not begin a flow: %d calls", remote.beginCalls)
	}

	// Without an in-process setup hook the row explains itself instead of
	// silently doing nothing.
	m2, _, _ := newModel(t)
	m2.remote = remote
	m2 = openAuthOverlay(t, m2)
	next, _ = pressAuthKey(m2, runeKey('j'))
	m2 = next
	next, _ = pressAuthKey(m2, runeKey('j'))
	m2 = next
	next, _ = pressAuthKey(m2, tea.KeyPressMsg{Code: tea.KeyEnter})
	m2 = next
	if m2.overlay != "auth" || !strings.Contains(authOverlayView(m2), "Jamendo setup is unavailable") {
		t.Fatalf("missing hook notice: overlay=%q\n%s", m2.overlay, authOverlayView(m2))
	}
}

func TestAuthOverlayFlowURLArrivesLate(t *testing.T) {
	remote := &fakeAuthRemote{
		list: defaultAuthList(),
		beginFlow: api.AuthorizationFlow{
			FlowID: "f4", Source: api.SourceAudius, Status: api.FlowPending,
			Interaction: api.Interaction{Type: api.InteractionBrowser},
		},
	}
	m, _, _ := newModel(t)
	m.remote = remote
	m = openAuthOverlay(t, m)
	next, _ := pressAuthKey(m, runeKey('j'))
	m = next
	m, _ = beginAuthFlowFrom(t, m)
	if view := authOverlayView(m); strings.Contains(view, "http") {
		t.Fatalf("URL row rendered before the flow published one:\n%s", view)
	}

	// The poll delivers the same pending flow, now with its URL.
	late := remote.beginFlow
	late.Interaction.URL = "https://audius.org/late"
	updated, follow := m.Update(authFlowMsg{flow: late})
	m = updated.(Model)
	if m.authFlow == nil || m.authFlow.Status != api.FlowPending {
		t.Fatalf("late URL dropped the pending flow: %+v", m.authFlow)
	}
	if follow == nil {
		t.Fatal("a still-pending flow must keep polling")
	}
	if view := authOverlayView(m); !strings.Contains(view, "https://audius.org/late") {
		t.Fatalf("late URL missing from the overlay:\n%s", view)
	}
}

func TestAuthOverlayEscCancelsPendingFlow(t *testing.T) {
	remote := &fakeAuthRemote{
		list: defaultAuthList(),
		beginFlow: api.AuthorizationFlow{
			FlowID: "f5", Source: api.SourceAppleMusic, Status: api.FlowPending,
			Interaction: api.Interaction{Type: api.InteractionSystemDialog},
		},
	}
	m, _, _ := newModel(t)
	m.remote = remote
	m = openAuthOverlay(t, m)
	m, _ = beginAuthFlowFrom(t, m)

	next, cmd := pressAuthKey(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	m = next
	if remote.cancelCalls != 0 || cmd == nil {
		t.Fatalf("Esc did not issue authorization.cancel: calls=%d cmd=%v", remote.cancelCalls, cmd != nil)
	}
	_ = cmd() // authCancelMsg
	if remote.cancelCalls != 1 {
		t.Fatalf("authorization.cancel calls = %d", remote.cancelCalls)
	}
	if m.authFlow != nil {
		t.Fatalf("local flow tracking survived the cancel: %+v", m.authFlow)
	}
	if m.overlay != "auth" {
		t.Fatalf("Esc during a flow must keep the overlay open: %q", m.overlay)
	}
	if view := authOverlayView(m); strings.Contains(view, "system authorization dialog") {
		t.Fatalf("progress row survived the cancel:\n%s", view)
	}

	// Without a pending flow, Esc closes.
	next, _ = pressAuthKey(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	m = next
	if m.overlay != "" {
		t.Fatalf("second Esc must close the overlay: %q", m.overlay)
	}
}

func TestAuthOverlayEscWithoutFlowCloses(t *testing.T) {
	m, _, _ := newModel(t)
	m.remote = &fakeAuthRemote{list: defaultAuthList()}
	m = openAuthOverlay(t, m)
	next, _ := pressAuthKey(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	m = next
	if m.overlay != "" {
		t.Fatalf("Esc did not close the overlay: %q", m.overlay)
	}
}

func TestAuthOverlayDisconnectRequiresTwoPresses(t *testing.T) {
	remote := &fakeAuthRemote{list: defaultAuthList()}
	m, _, _ := newModel(t)
	m.remote = remote
	m = openAuthOverlay(t, m)

	next, cmd := pressAuthKey(m, runeKey('d'))
	m = next
	if remote.disconnectCalls != 0 || cmd != nil {
		t.Fatalf("first d must only confirm: calls=%d cmd=%v", remote.disconnectCalls, cmd != nil)
	}
	if view := authOverlayView(m); !strings.Contains(view, "Disconnect Apple Music? Press d again to confirm") {
		t.Fatalf("confirmation row missing:\n%s", view)
	}

	next, cmd = pressAuthKey(m, runeKey('d'))
	m = next
	message := cmd()
	disconnect, ok := message.(authDisconnectMsg)
	if !ok {
		t.Fatalf("second d did not run authorization.disconnect: %#v", message)
	}
	if remote.disconnectCalls != 1 || remote.disconnectSource != "apple-music" {
		t.Fatalf("disconnect = %d calls for %q", remote.disconnectCalls, remote.disconnectSource)
	}
	if disconnect.err != nil {
		t.Fatalf("disconnect failed: %v", disconnect.err)
	}
	updated, refresh := m.Update(disconnect)
	m = updated.(Model)
	if !strings.Contains(authOverlayView(m), "Apple Music disconnected") {
		t.Fatalf("disconnect feedback missing:\n%s", authOverlayView(m))
	}
	m = drainAuthFetch(t, m, refresh)
	if remote.listCalls != 2 {
		t.Fatalf("disconnect did not re-read the list: %d calls", remote.listCalls)
	}
}

func TestAuthOverlayDisconnectConfirmClearedByMove(t *testing.T) {
	remote := &fakeAuthRemote{list: defaultAuthList()}
	m, _, _ := newModel(t)
	m.remote = remote
	m = openAuthOverlay(t, m)

	next, _ := pressAuthKey(m, runeKey('d'))
	m = next
	next, _ = pressAuthKey(m, runeKey('j'))
	m = next
	if m.authConfirm != "" {
		t.Fatalf("moving the cursor must cancel the confirmation: %q", m.authConfirm)
	}
	next, _ = pressAuthKey(m, runeKey('d'))
	m = next
	if remote.disconnectCalls != 0 {
		t.Fatalf("d on a new row executed the old confirmation: %d calls", remote.disconnectCalls)
	}
	if view := authOverlayView(m); !strings.Contains(view, "Disconnect Audius? Press d again to confirm") {
		t.Fatalf("confirmation did not follow the cursor:\n%s", view)
	}
}

// A source the server cannot disconnect hides the action: `d` shows a plain
// notice and never reaches the server, and the hint drops the key (OQ40).
func TestAuthOverlayHidesDisconnectForUnsupportedSource(t *testing.T) {
	remote := &fakeAuthRemote{list: defaultAuthList()}
	m, _, _ := newModel(t)
	m.remote = remote
	m = openAuthOverlay(t, m)
	for i := 0; i < len(defaultAuthList())-1; i++ {
		next, _ := pressAuthKey(m, runeKey('j'))
		m = next
	}
	if view := authOverlayView(m); strings.Contains(view, "d disconnect") {
		t.Fatalf("hint offered disconnect on the radio row:\n%s", view)
	}
	next, cmd := pressAuthKey(m, runeKey('d'))
	m = next
	if cmd != nil || remote.disconnectCalls != 0 {
		t.Fatalf("d on an unsupported row must not reach the server: cmd=%v calls=%d", cmd != nil, remote.disconnectCalls)
	}
	if m.authConfirm != "" {
		t.Fatalf("d armed a confirmation on an unsupported row: %q", m.authConfirm)
	}
	if !strings.Contains(authOverlayView(m), "Disconnect is not available for Radio") {
		t.Fatalf("missing unsupported notice:\n%s", authOverlayView(m))
	}
}

func TestAuthOverlayAuthorizationChangedRefreshesList(t *testing.T) {
	remote := &fakeAuthRemote{list: defaultAuthList()}
	m, _, _ := newModel(t)
	m.remote = remote
	m = openAuthOverlay(t, m)
	if view := authOverlayView(m); !strings.Contains(view, "not signed in") {
		t.Fatalf("initial list missing:\n%s", view)
	}

	// The server publishes apple-music's settlement; the overlay re-reads
	// the whole list instead of patching one row.
	remote.list = []api.SourceAuthorization{
		{Source: api.SourceAppleMusic, Status: api.AuthAuthorized, Details: map[string]any{"accountStatus": "ready"}},
		{Source: api.SourceAudius, Status: api.AuthAuthorized, AccountLabel: "guocai"},
		{Source: api.SourceJamendo, Status: api.AuthNotRequired},
		{Source: api.SourceRadio, Status: api.AuthNotRequired},
	}
	next, _ := m.applyWatchUpdate(api.WatchUpdate{
		Kind:          "authorization.changed",
		Sequence:      m.sequence + 1,
		Authorization: &api.SourceAuthorization{Source: api.SourceAppleMusic, Status: api.AuthAuthorized},
	})
	m = next.(Model)
	if !m.authListLoading {
		t.Fatal("authorization.changed did not trigger a list re-read while the overlay is open")
	}
	// Apply the refreshed fetch (the returned batch also re-arms the watch
	// feed, which blocks on its channel and is never run here).
	next, _ = m.Update(authListMsg{fetch: m.authFetch, authorizations: remote.list})
	m = next.(Model)
	view := authOverlayView(m)
	if !strings.Contains(view, authRow("Apple Music", "authorized")) {
		t.Fatalf("refreshed status missing:\n%s", view)
	}
	if strings.Contains(view, "not signed in") {
		t.Fatalf("stale status survived the refresh:\n%s", view)
	}
}

func TestAuthOverlayBeginErrorShowsRow(t *testing.T) {
	remote := &fakeAuthRemote{
		list:     defaultAuthList(),
		beginErr: api.Errorf(api.CodeAuthorizationInProgress, "the source already has an active flow"),
	}
	m, _, _ := newModel(t)
	m.remote = remote
	m = openAuthOverlay(t, m)

	next, cmd := pressAuthKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next
	message := cmd()
	begin, ok := message.(authBeginMsg)
	if !ok || begin.err == nil {
		t.Fatalf("unexpected begin message %#v", message)
	}
	updated, _ := m.Update(begin)
	m = updated.(Model)
	if m.overlay != "auth" {
		t.Fatalf("a failed begin must keep the overlay open: %q", m.overlay)
	}
	// The notice keeps the full message; the rendered row may clip it to the
	// overlay width, so the view assertion uses the stable prefix and code.
	if !strings.Contains(m.authNotice, "the source already has an active flow") {
		t.Fatalf("begin error notice = %q", m.authNotice)
	}
	view := authOverlayView(m)
	for _, want := range []string{"Sign-in failed:", "authorization_in_progress"} {
		if !strings.Contains(view, want) {
			t.Fatalf("begin error missing %q:\n%s", want, view)
		}
	}
	if !m.authNoticeErr {
		t.Fatal("a failed begin must mark the notice as an error")
	}
	if m.authBusy || m.authFlow != nil {
		t.Fatalf("failed begin left operation state: busy=%v flow=%v", m.authBusy, m.authFlow)
	}
}

func TestAuthOverlayFlowTerminalStopsPollingAndRefreshes(t *testing.T) {
	remote := &fakeAuthRemote{
		list: defaultAuthList(),
		beginFlow: api.AuthorizationFlow{
			FlowID: "f6", Source: api.SourceAppleMusic, Status: api.FlowPending,
			Interaction: api.Interaction{Type: api.InteractionSystemDialog},
		},
	}
	m, _, _ := newModel(t)
	m.remote = remote
	m = openAuthOverlay(t, m)
	m, poll := beginAuthFlowFrom(t, m)
	if poll == nil {
		t.Fatal("pending flow did not schedule polling")
	}

	// The server settles the flow; the poll result is terminal.
	terminal := remote.beginFlow
	terminal.Status = api.FlowAuthorized
	next, refresh := m.Update(authFlowMsg{flow: terminal})
	m = next.(Model)
	if m.authFlow != nil {
		t.Fatalf("terminal flow kept progress state: %+v", m.authFlow)
	}
	if !strings.Contains(authOverlayView(m), "Apple Music authorized") {
		t.Fatalf("terminal outcome missing:\n%s", authOverlayView(m))
	}
	m = drainAuthFetch(t, m, refresh)
	if view := authOverlayView(m); !strings.Contains(view, authRow("Apple Music", "not signed in")) {
		// The canned list still says not signed in; what matters is that the
		// re-read replaced nothing silently and the progress row is gone.
		t.Fatalf("list re-read missing after terminal state:\n%s", view)
	}
	if strings.Contains(authOverlayView(m), "system authorization dialog") {
		t.Fatalf("progress row survived a terminal flow:\n%s", authOverlayView(m))
	}
}

func TestAuthOverlayFlowErrorTerminalShowsErrorRow(t *testing.T) {
	remote := &fakeAuthRemote{
		list: defaultAuthList(),
		beginFlow: api.AuthorizationFlow{
			FlowID: "f7", Source: api.SourceAudius, Status: api.FlowPending,
			Interaction: api.Interaction{Type: api.InteractionBrowser, URL: "https://audius.org/link"},
		},
	}
	m, _, _ := newModel(t)
	m.remote = remote
	m = openAuthOverlay(t, m)
	next, _ := pressAuthKey(m, runeKey('j'))
	m = next
	m, _ = beginAuthFlowFrom(t, m)

	terminal := remote.beginFlow
	terminal.Status = api.FlowError
	terminal.Error = api.Errorf(api.CodeAuthorizationFailed, "Audius rejected the profile")
	updated, _ := m.Update(authFlowMsg{flow: terminal})
	m = updated.(Model)
	view := authOverlayView(m)
	for _, want := range []string{"Audius sign-in failed", "Audius rejected the profile"} {
		if !strings.Contains(view, want) {
			t.Fatalf("error outcome missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "waiting for sign-in") {
		t.Fatalf("progress row survived an error terminal state:\n%s", view)
	}
}

func TestAuthOverlayFlowNotFoundStopsWaitingTransientErrorKeepsPolling(t *testing.T) {
	remote := &fakeAuthRemote{
		list: defaultAuthList(),
		beginFlow: api.AuthorizationFlow{
			FlowID: "f8", Source: api.SourceAppleMusic, Status: api.FlowPending,
			Interaction: api.Interaction{Type: api.InteractionSystemDialog},
		},
	}
	m, _, _ := newModel(t)
	m.remote = remote
	m = openAuthOverlay(t, m)
	m, _ = beginAuthFlowFrom(t, m)

	// A transient transport failure keeps waiting: the flow is server-owned.
	next, follow := m.Update(authFlowMsg{err: errors.New("i/o timeout")})
	m = next.(Model)
	if m.authFlow == nil || follow == nil {
		t.Fatal("a transient poll error dropped the pending flow")
	}

	// An unknown flow id means the flow is gone; stop instead of polling
	// forever.
	next, refresh := m.Update(authFlowMsg{err: api.Errorf(api.CodeAuthorizationFlowNotFound, "the flow id is unknown")})
	m = next.(Model)
	if m.authFlow != nil {
		t.Fatalf("unknown flow kept progress state: %+v", m.authFlow)
	}
	if !strings.Contains(authOverlayView(m), "The sign-in flow no longer exists") {
		t.Fatalf("missing flow notice:\n%s", authOverlayView(m))
	}
	m = drainAuthFetch(t, m, refresh)
}

func TestAuthOverlayListLoadErrorShowsRowAndKeepsStaleRows(t *testing.T) {
	remote := &fakeAuthRemote{list: defaultAuthList()}
	m, _, _ := newModel(t)
	m.remote = remote
	m = openAuthOverlay(t, m)

	// A refresh failure after a successful read keeps the old rows visible
	// and explains the failure.
	remote.listErr = errors.New("socket busy")
	next, _ := m.applyWatchUpdate(api.WatchUpdate{
		Kind:          "authorization.changed",
		Sequence:      m.sequence + 1,
		Authorization: &api.SourceAuthorization{Source: api.SourceRadio, Status: api.AuthNotRequired},
	})
	m = next.(Model)
	next, _ = m.Update(authListMsg{fetch: m.authFetch, err: remote.listErr})
	m = next.(Model)
	view := authOverlayView(m)
	for _, want := range []string{"Unable to load authorizations: socket busy", authRow("Audius", "linked · guocai")} {
		if !strings.Contains(view, want) {
			t.Fatalf("refresh failure missing %q:\n%s", want, view)
		}
	}

	// A first-read failure shows only the error row, and the overlay stays
	// open for Esc or another attempt.
	m2, _, _ := newModel(t)
	m2.remote = &fakeAuthRemote{listErr: errors.New("socket busy")}
	next, cmd := m2.runPaletteCommand("auth")
	m2 = next.(Model)
	m2 = drainAuthFetch(t, m2, cmd)
	view = authOverlayView(m2)
	if !strings.Contains(view, "Unable to load authorizations: socket busy") {
		t.Fatalf("first-read failure missing the error row:\n%s", view)
	}
	if m2.overlay != "auth" {
		t.Fatalf("a failed read closed the overlay: %q", m2.overlay)
	}
}

func TestAuthOverlayClickOutsideDismissesWithoutCancellingFlow(t *testing.T) {
	remote := &fakeAuthRemote{
		list: defaultAuthList(),
		beginFlow: api.AuthorizationFlow{
			FlowID: "f9", Source: api.SourceAppleMusic, Status: api.FlowPending,
			Interaction: api.Interaction{Type: api.InteractionSystemDialog},
		},
	}
	m, _, _ := newModel(t)
	m.remote = remote
	m = openAuthOverlay(t, m)
	m, _ = beginAuthFlowFrom(t, m)

	// Clicks outside cancel the overlay (cancelOverlay), not the flow: the
	// flow is server-owned and may still complete — Esc is the explicit cancel.
	m = m.cancelOverlay()
	if m.overlay != "" || m.authFlow != nil {
		t.Fatalf("dismiss did not drop local flow tracking: overlay=%q flow=%v", m.overlay, m.authFlow)
	}
	if remote.cancelCalls != 0 {
		t.Fatalf("dismissal cancelled the server flow: %d calls", remote.cancelCalls)
	}
}

func TestAuthOverlayClickSelectsThenActivates(t *testing.T) {
	remote := &fakeAuthRemote{
		list:      defaultAuthList(),
		beginFlow: api.AuthorizationFlow{FlowID: "f10", Source: api.SourceAudius, Status: api.FlowPending},
	}
	m, _, _ := newModel(t)
	m.remote = remote
	m = openAuthOverlay(t, m)

	// A click on another row selects it; a second click on the same row
	// activates it like Enter, running authorization.begin through the
	// returned command like every model command.
	next, _ := m.handleOverlayClick(1, 2)
	m = next.(Model)
	if m.authSelected != 1 {
		t.Fatalf("click selected row %d, want 1", m.authSelected)
	}
	updated, beginCmd := m.handleOverlayClick(1, 2)
	if beginCmd == nil {
		t.Fatal("second click did not activate the row")
	}
	message := beginCmd()
	if _, ok := message.(authBeginMsg); !ok {
		t.Fatalf("unexpected activation message %#v", message)
	}
	if remote.beginCalls != 1 || remote.beginSource != "audius" {
		t.Fatalf("second click did not begin audius: calls=%d source=%q", remote.beginCalls, remote.beginSource)
	}
	m = updated.(Model)

	// A click below the rows (progress or hint row) does nothing.
	next, _ = m.handleOverlayClick(1, 9)
	m = next.(Model)
	if m.authSelected != 1 || remote.beginCalls != 1 {
		t.Fatalf("a click past the rows changed state: selected=%d begins=%d", m.authSelected, remote.beginCalls)
	}
}

// The disconnect failure notice speaks to a person: the stable code stays in
// the journal and details, the overlay shows the sanitized message.
func TestDisconnectFailureTextUsesTheHumanMessage(t *testing.T) {
	err := api.Errorf(api.CodeUnsupportedCommand, "macOS does not allow disconnecting Apple Music")
	if got := disconnectFailureText(err); got != "macOS does not allow disconnecting Apple Music" {
		t.Fatalf("notice = %q", got)
	}
	if got := disconnectFailureText(errors.New("plain failure")); got != "plain failure" {
		t.Fatalf("plain notice = %q", got)
	}
}

// A long disconnect reason must be read in full: the notice wraps to the box
// width instead of being clipped at the border.
func TestDisconnectNoticeWrapsLongReason(t *testing.T) {
	m, _, _ := newModel(t)
	m.overlay = "auth"
	m.authListLoaded = true
	m.authList = []api.SourceAuthorization{{Source: api.SourceAppleMusic, Status: api.AuthAuthorized}}
	m.authSelected = 0
	m.authNotice = "Disconnect failed: macOS does not allow programmatic Apple Music disconnect from the client"
	m.authNoticeErr = true
	m.width, m.height = 110, 30
	rows := m.authOverlayRows(60)
	joined := ""
	for i, row := range rows {
		if i >= len(rows)-3 {
			joined += strings.TrimSpace(plainText(row)) + " "
		}
	}
	if !strings.Contains(joined, "programmatic Apple Music disconnect") || strings.Contains(joined, "Mu…") {
		t.Fatalf("notice was clipped instead of wrapped:\n%s", joined)
	}
	for _, row := range rows {
		if w := lipgloss.Width(plainText(row)); w > 62 {
			t.Fatalf("notice row exceeds the box: %d %q", w, plainText(row))
		}
	}
}
