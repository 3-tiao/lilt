package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/presentation"
)

// authFlowPollInterval matches the CLI's awaitAuthFlow cadence: a browser
// flow's URL may arrive after the begin response, and the server alone decides
// when the flow reaches a terminal state.
const authFlowPollInterval = time.Second

// openAuthOverlay opens the Account overlay (docs/ui/model.md §10): one row
// per declared source with its live authorization status. It is the actionable
// version of the account summary — the toast path it replaces could only
// restate that summary. The cursor starts on the browsing source's row.
func (m Model) openAuthOverlay() (tea.Model, tea.Cmd) {
	m.overlay = "auth"
	m.authSelected = max(0, indexOf(m.authRowSources(), m.source))
	m.authConfirm = ""
	m.authNotice, m.authNoticeErr = "", false
	m.authFlow = nil
	m.authBusy = false
	return m, m.fetchAuthList()
}

// fetchAuthList re-reads authorization.list. Each request bumps the fetch
// counter, so a late response can never replace a newer list (the same
// generation discipline the discovery views use).
func (m *Model) fetchAuthList() tea.Cmd {
	if m.remote == nil {
		return nil
	}
	m.authFetch++
	m.authListLoading = true
	fetch := m.authFetch
	remote := m.remote
	return func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		authorizations, err := remote.AuthList(ctx)
		return authListMsg{fetch: fetch, authorizations: authorizations, err: err}
	}
}

func (m Model) applyAuthList(msg authListMsg) (tea.Model, tea.Cmd) {
	if m.overlay != "auth" || msg.fetch < m.authFetch {
		return m, nil
	}
	m.authListLoading = false
	if msg.err != nil {
		// Rows from the previous successful read stay visible; the error row
		// explains what failed instead of blanking the overlay.
		m.authListErr = "Unable to load authorizations: " + presentation.Text(msg.err.Error())
		return m, nil
	}
	m.authListErr = ""
	m.authList = append([]api.SourceAuthorization(nil), msg.authorizations...)
	m.authListLoaded = true
	m.authSelected = clamp(m.authSelected, 0, max(0, len(m.authRowSources())-1))
	return m, nil
}

// authRowSources lists the overlay's rows in the server's descriptor order,
// falling back to the authorization list's own order before descriptors load.
func (m Model) authRowSources() []string {
	if len(m.descriptors) > 0 {
		sources := make([]string, 0, len(m.descriptors))
		for _, descriptor := range m.descriptors {
			sources = append(sources, string(descriptor.ID))
		}
		return sources
	}
	sources := make([]string, 0, len(m.authList))
	for _, authorization := range m.authList {
		sources = append(sources, string(authorization.Source))
	}
	return sources
}

// activateAuthRow runs the selected row's Enter action. Enter is dispatched by
// source: apple-music and audius start an authorization flow, jamendo opens the
// in-process setup modal (there is no flow to begin), and sources without an
// authorization flow (radio) have no action.
func (m Model) activateAuthRow() (tea.Model, tea.Cmd) {
	sources := m.authRowSources()
	if len(sources) == 0 {
		return m, nil
	}
	source := sources[clamp(m.authSelected, 0, len(sources)-1)]
	m.authConfirm = ""
	switch source {
	case string(api.SourceJamendo):
		if m.jamendoSetup == nil {
			m.authNotice, m.authNoticeErr = "Jamendo setup is unavailable in this session", true
			return m, nil
		}
		return m.openJamendoSetup()
	case string(api.SourceAppleMusic), string(api.SourceAudius):
		return m.beginAuthFlow(source)
	default:
		return m, nil
	}
}

// beginAuthFlow starts authorization.begin for one source. The server answers
// immediately with a flow (usually pending); the progress line and every later
// state come from flowStatus polling, never from this call.
func (m Model) beginAuthFlow(source string) (tea.Model, tea.Cmd) {
	if m.authBusy {
		m.authNotice, m.authNoticeErr = "An account action is still running", true
		return m, nil
	}
	if m.authFlow != nil && m.authFlow.Status == api.FlowPending {
		m.authNotice, m.authNoticeErr = "A sign-in is already in progress — Esc cancels it", true
		return m, nil
	}
	if m.remote == nil {
		m.authNotice, m.authNoticeErr = "No server connection", true
		return m, nil
	}
	m.authBusy = true
	m.authNotice, m.authNoticeErr = "", false
	m.logEvent("auth", map[string]any{"action": "begin", "authSource": source})
	remote := m.remote
	return m, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		flow, err := remote.BeginAuth(ctx, source)
		return authBeginMsg{source: source, flow: flow, err: err}
	}
}

func (m Model) applyAuthBegin(msg authBeginMsg) (tea.Model, tea.Cmd) {
	if m.overlay != "auth" || !m.authBusy {
		return m, nil
	}
	m.authBusy = false
	if msg.err != nil {
		// A failed begin (for example the source already has an active flow)
		// must be visible inside the overlay, not silent.
		m.authNotice, m.authNoticeErr = "Sign-in failed: "+presentation.Text(msg.err.Error()), true
		return m, nil
	}
	if msg.flow.Status == api.FlowPending {
		flow := msg.flow
		m.authFlow = &flow
		return m, m.pollAuthFlow()
	}
	// A begin can return an already-terminal flow (for example an authorized
	// source): there is nothing to wait for, so name the outcome and re-read.
	notice, isErr := authFlowOutcome(msg.flow)
	m.authNotice, m.authNoticeErr = notice, isErr
	return m, m.fetchAuthList()
}

// pollAuthFlow schedules the next flowStatus read for the pending flow. The
// chain re-arms itself from each pending result; terminal states, cancellation
// and closing the overlay all stop it.
func (m Model) pollAuthFlow() tea.Cmd {
	if m.remote == nil || m.authFlow == nil {
		return nil
	}
	flowID := m.authFlow.FlowID
	remote := m.remote
	return tea.Tick(authFlowPollInterval, func(time.Time) tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		flow, err := remote.FlowStatus(ctx, flowID)
		return authFlowMsg{flow: flow, err: err}
	})
}

func (m Model) applyAuthFlow(msg authFlowMsg) (tea.Model, tea.Cmd) {
	if m.overlay != "auth" || m.authFlow == nil {
		return m, nil
	}
	if msg.err != nil {
		var apiErr *api.Error
		if errors.As(msg.err, &apiErr) && apiErr.Code == api.CodeAuthorizationFlowNotFound {
			// The flow no longer exists (server restart, aged out). Stop
			// waiting on it instead of polling forever.
			m.authFlow = nil
			m.authNotice, m.authNoticeErr = "The sign-in flow no longer exists", true
			return m, m.fetchAuthList()
		}
		// A single transient poll failure keeps waiting: the flow is
		// server-owned and outlives any one response.
		return m, m.pollAuthFlow()
	}
	if msg.flow.FlowID != m.authFlow.FlowID {
		return m, nil
	}
	if msg.flow.Status == api.FlowPending {
		flow := msg.flow
		m.authFlow = &flow
		return m, m.pollAuthFlow()
	}
	notice, isErr := authFlowOutcome(msg.flow)
	m.authFlow = nil
	m.authNotice, m.authNoticeErr = notice, isErr
	return m, m.fetchAuthList()
}

// cancelAuthFlow is Esc's meaning while a flow is pending: authorization.cancel
// with the flow id. The request is optimistic — local tracking clears now, and
// the refreshed list shows the truth whether the cancel succeeds or fails.
func (m Model) cancelAuthFlow() (tea.Model, tea.Cmd) {
	if m.remote == nil || m.authFlow == nil || m.authFlow.Status != api.FlowPending {
		return m, nil
	}
	flowID := m.authFlow.FlowID
	m.authFlow = nil
	remote := m.remote
	m.logEvent("auth", map[string]any{"action": "cancel", "flowID": flowID})
	return m, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		flow, err := remote.CancelAuth(ctx, flowID)
		return authCancelMsg{flow: flow, err: err}
	}
}

func (m Model) applyAuthCancel(msg authCancelMsg) (tea.Model, tea.Cmd) {
	if m.overlay != "auth" {
		return m, nil
	}
	if msg.err != nil {
		m.authNotice, m.authNoticeErr = "Cancel failed: "+presentation.Text(msg.err.Error()), true
	}
	return m, m.fetchAuthList()
}

// toggleAuthDisconnect is `d` on the selected row. Disconnect is the overlay's
// destructive action (it removes the machine's stored credential), so the
// first press arms a confirmation row and only the second executes; moving
// the selection cancels it.
func (m Model) toggleAuthDisconnect() (tea.Model, tea.Cmd) {
	sources := m.authRowSources()
	if len(sources) == 0 {
		return m, nil
	}
	source := sources[clamp(m.authSelected, 0, len(sources)-1)]
	if m.authBusy {
		m.authNotice, m.authNoticeErr = "An account action is still running", true
		return m, nil
	}
	if m.authConfirm != source {
		m.authConfirm = source
		m.authNotice, m.authNoticeErr = "", false
		return m, nil
	}
	m.authConfirm = ""
	if m.remote == nil {
		m.authNotice, m.authNoticeErr = "No server connection", true
		return m, nil
	}
	m.authBusy = true
	m.authNotice, m.authNoticeErr = "", false
	m.logEvent("auth", map[string]any{"action": "disconnect", "authSource": source})
	remote := m.remote
	return m, func() tea.Msg {
		ctx, cancel := boundedContext()
		defer cancel()
		_, err := remote.DisconnectAuth(ctx, source)
		return authDisconnectMsg{source: source, err: err}
	}
}

func (m Model) applyAuthDisconnect(msg authDisconnectMsg) (tea.Model, tea.Cmd) {
	if m.overlay != "auth" || !m.authBusy {
		return m, nil
	}
	m.authBusy = false
	if msg.err != nil {
		// The overlay speaks to a person: the stable code stays machine-facing
		// (journal, details), the notice shows the sanitized message so a long
		// reason is not eaten by the code prefix (batch 2026-09-28-rounds F5).
		m.authNotice, m.authNoticeErr = "Disconnect failed: "+disconnectFailureText(msg.err), true
		return m, m.fetchAuthList()
	}
	m.authNotice, m.authNoticeErr = sourceTitle(msg.source)+" disconnected", false
	return m, m.fetchAuthList()
}

// disconnectFailureText keeps the overlay notice human-readable: the stable
// error code is machine-facing (journal and details carry it), so the message
// is shown without the "code: " prefix that would eat the row budget.
func disconnectFailureText(err error) string {
	var apiErr *api.Error
	if errors.As(err, &apiErr) && apiErr != nil && apiErr.Message != "" {
		return presentation.Text(apiErr.Message)
	}
	return presentation.Text(err.Error())
}

// moveAuthSelection moves the overlay cursor; leaving the armed row cancels
// the pending disconnect confirmation.
func (m Model) moveAuthSelection(delta int) Model {
	sources := m.authRowSources()
	if len(sources) == 0 {
		return m
	}
	previous := m.authSelected
	m.authSelected = clamp(m.authSelected+delta, 0, len(sources)-1)
	if m.authSelected != previous {
		m.authConfirm = ""
	}
	return m
}

// openAuthFlowURL is the manual launcher for a browser flow's sign-in URL —
// the same key precedent as the Jamendo setup modal's devportal link. The
// overlay never auto-opens a browser; the user starts it.
func (m Model) openAuthFlowURL() (tea.Model, tea.Cmd) {
	if m.authFlow == nil || m.authFlow.Interaction.URL == "" || m.openURL == nil {
		return m, nil
	}
	url := m.authFlow.Interaction.URL
	open := m.openURL
	m.logEvent("auth", map[string]any{"event": "open-url", "authSource": string(m.authFlow.Source)})
	return m, func() tea.Msg { open(url); return nil }
}

// authOverlayRows lays out the Account overlay body in the design system's
// band grammar: the per-source rows, the in-flight flow progress, the pending
// disconnect confirmation, the latest feedback, and the key hints. The header
// keeps its stable identity ("Account"); every transient state is a body row
// (docs/ui/design-system.md §3).
func (m Model) authOverlayRows(inner int) []string {
	dimStyle, loadingStyle := m.renderer.dimStyle, m.renderer.loadingStyle
	sources := m.authRowSources()
	rows := make([]string, 0, len(sources)+4)
	if m.authListErr != "" {
		rows = append(rows, m.renderer.errorStyle.Render(fit(m.authListErr, inner)))
	}
	switch {
	case len(sources) > 0 && m.authListLoaded:
		for i, source := range sources {
			marker, style := "  ", m.renderer.rowStyle
			if i == m.authSelected {
				style, marker = m.renderer.selStyle, "› "
			}
			row := fmt.Sprintf("%-12s %s", sourceTitle(source), m.authRowStatus(source))
			rows = append(rows, style.Render(fit(marker+row, inner)))
		}
	case m.authListErr == "":
		rows = append(rows, loadingStyle.Render(fit("loading…", inner)))
	}
	if m.authFlow != nil {
		rows = append(rows, m.authFlowRows(inner)...)
	}
	if m.authConfirm != "" {
		rows = append(rows, m.renderer.errorStyle.Render(fit("Disconnect "+sourceTitle(m.authConfirm)+"? Press d again to confirm", inner)))
	}
	if m.authNotice != "" {
		style := m.renderer.accentStyle
		if m.authNoticeErr {
			style = m.renderer.errorStyle
		}
		// A long reason must be read in full: wrap instead of clipping at the
		// border (batch 2026-09-28-rounds F5). The box height is derived from
		// these rows, so the overlay grows with the notice.
		for _, row := range wrapRows(m.authNotice, inner) {
			rows = append(rows, style.Render(row))
		}
	}
	hint := "j/k move · Enter sign in / setup · d disconnect · Esc close"
	if m.authFlow != nil {
		hint = "Esc cancel sign-in"
		if m.authFlow.Interaction.URL != "" {
			hint = "ctrl+o open link · Esc cancel sign-in"
		}
	}
	rows = append(rows, dimStyle.Render(fit(hint, inner)))
	return rows
}

// wrapRows splits text into rows that fit width, breaking on spaces. It is the
// shared wrap for overlay notices that must stay readable in full.
func wrapRows(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	rows := make([]string, 0, 2)
	current := ""
	for _, word := range words {
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if current != "" && lipgloss.Width(candidate) > width {
			rows = append(rows, current)
			current = word
			continue
		}
		current = candidate
	}
	if current != "" {
		rows = append(rows, current)
	}
	return rows
}

// authRowStatus summarizes one row's live authorization.
func (m Model) authRowStatus(source string) string {
	for _, authorization := range m.authList {
		if string(authorization.Source) == source {
			return authStatusText(source, authorization)
		}
	}
	return "…"
}

// authStatusText renders one source's wire authorization as a human status.
// The per-source wording reuses the account summary's semantics; an
// unexpected status shows through as its raw value instead of being masked.
func authStatusText(source string, authorization api.SourceAuthorization) string {
	projected := api.ProjectAuthorization(authorization)
	switch source {
	case string(api.SourceAppleMusic):
		switch projected.Status {
		case api.AuthAuthorized:
			switch projected.AccountStatus {
			case "subscription_required":
				return "authorized · subscription required"
			case "cloud_library_disabled":
				return "authorized · Sync Library off"
			case "account_unavailable":
				return "authorized · account unavailable"
			}
			return "authorized"
		case api.AuthNotDetermined:
			return "not signed in"
		case api.AuthDenied:
			return "access denied"
		case api.AuthPending:
			return "sign-in pending"
		}
	case string(api.SourceAudius):
		switch projected.Status {
		case api.AuthAuthorized:
			if projected.AccountLabel != "" {
				return "linked · " + projected.AccountLabel
			}
			return "linked"
		case api.AuthNotDetermined:
			return "not linked"
		case api.AuthExpired:
			return "link expired"
		case api.AuthPending:
			return "sign-in pending"
		}
	case string(api.SourceRadio), string(api.SourceJamendo):
		if projected.Status == api.AuthNotRequired {
			return "not required"
		}
	}
	return projected.Status
}

// authFlowRows renders the pending flow's progress lines. The wording is
// driven by the wire Interaction.Type — the helper's system dialog and the
// browser engine's login page are two server-declared interactions, not two
// client platforms.
func (m Model) authFlowRows(inner int) []string {
	flow := *m.authFlow
	title := sourceTitle(string(flow.Source))
	if flow.Interaction.Type == api.InteractionSystemDialog {
		return []string{m.renderer.loadingStyle.Render(fit(title+" — complete the system authorization dialog…", inner))}
	}
	rows := []string{}
	if flow.Interaction.URL != "" {
		rows = append(rows, m.renderer.rowStyle.Render(fit(flow.Interaction.URL, inner)))
	}
	if flow.Interaction.UserCode != "" {
		rows = append(rows, m.renderer.rowStyle.Render(fit("code: "+flow.Interaction.UserCode, inner)))
	}
	return append(rows, m.renderer.loadingStyle.Render(fit(title+" — waiting for sign-in…", inner)))
}

// authFlowOutcome maps a terminal flow to the overlay's feedback row. The
// server decides terminal states; the overlay only names what happened.
func authFlowOutcome(flow api.AuthorizationFlow) (string, bool) {
	title := sourceTitle(string(flow.Source))
	switch flow.Status {
	case api.FlowAuthorized:
		return title + " authorized", false
	case api.FlowCancelled:
		return title + " sign-in cancelled", false
	case api.FlowDenied:
		return title + " sign-in denied", true
	case api.FlowExpired:
		return title + " sign-in expired", true
	case api.FlowError:
		text := title + " sign-in failed"
		if flow.Error != nil {
			text += ": " + presentation.Text(flow.Error.Message)
		}
		return text, true
	}
	return "", false
}
