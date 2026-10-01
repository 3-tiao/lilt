// Package server hosts the single lilt server: it owns the playback engine,
// the authoritative state, the source registry, and the Client API socket.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/3-tiao/lilt/internal/activity"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/audius"
	"github.com/3-tiao/lilt/internal/icy"
	"github.com/3-tiao/lilt/internal/jamendo"
	"github.com/3-tiao/lilt/internal/radio"
	"github.com/3-tiao/lilt/internal/securestore"
	"github.com/3-tiao/lilt/internal/state"
)

// Options configures a Server. Only SocketPath is required; tests supply a fake
// Engine and an isolated Store/RadioCache.
type Options struct {
	SocketPath string
	LockPath   string
	Engine     Engine
	// EngineFactory, when set, builds a fresh active MusicKit playback backend
	// and enables automatic rebuild after its transport fails. Engine is used
	// only when no factory is given.
	EngineFactory func() (Engine, error)
	// AppleResourceFactory starts the independent, read-only MusicKit resource
	// client used for Apple discovery, library reads, and ref resolution. Its
	// lifetime is deliberately independent of the active playback backend.
	AppleResourceFactory func() (AppleResourceClient, error)
	// AudioEngineFactory starts lilt-audio on demand and rebuilds it after a
	// transport failure. AudioEngine is the deterministic-test alternative.
	AudioEngine        AudioEngine
	AudioEngineFactory func() (AudioEngine, error)
	Store              *state.Store
	Radio              *radio.Client
	RadioCache         *radio.Cache
	ICY                *icy.Client
	// ActivityPath is the SQLite activity database (favorites, history, derived
	// recent). Empty derives it next to the socket; a store that cannot open
	// degrades the server instead of failing startup.
	ActivityPath string
	// QueuePacing is the gap between finite-queue appends on the MusicKit
	// engine. Zero uses the default. It is set through LILT_QUEUE_PACING_MS;
	// the paced fill itself only runs on the append fallback (a rejected
	// one-shot batch), so the knob exists for probing that path.
	QueuePacing time.Duration
	// AuthProviders add or override authorization providers by source. Apple
	// and radio are registered automatically; tests pass a scriptable fixture.
	AuthProviders []AuthProvider
	// Providers replace or extend compiled-in content providers. This keeps
	// HTTP discovery hermetic in socket tests.
	Providers     []ContentProvider
	AudiusClient  *audius.Client
	JamendoClient *jamendo.Client
	// SecureStore holds provider credentials. Tests default to an in-memory
	// store; production passes the platform Keychain.
	SecureStore       securestore.Store
	URLPlaybackDriver URLPlaybackDriver

	// RecentMin caps the recent-history threshold; tests set it small. Zero
	// means the documented 30s.
	RecentMin time.Duration

	DedupBodies    int
	DedupEntries   int
	DedupTombstone time.Duration
	// AdmissionWait overrides the catalog's admission budget. Tests set it
	// small; production leaves it zero so the catalog stays the single source.
	AdmissionWait time.Duration

	Log func(kind string, fields map[string]any)
	// DebugLog receives high-fidelity diagnostics (full params/results, watch
	// events) when the operator enabled the debug level. It is a no-op unless
	// wired to journal.Debug; credentials are still redacted by the journal.
	DebugLog func(kind string, fields map[string]any)
	// QueueUndoWindow and Now are test seams for the transient queue-removal
	// receipt. Zero/nil select the production 5s window and wall clock.
	QueueUndoWindow time.Duration
	Now             func() time.Time
}

// Server is the single owner of playback, queue, and persisted state.
type Server struct {
	path     string
	registry *api.Registry
	dedup    *dedupCache
	// admission is the FIFO order for public side-effecting commands; see
	// admissionGate. admissionWait overrides the catalog budget in tests.
	admission     *admissionGate
	admissionWait time.Duration

	// instanceID identifies this server process. It is generated at Start and is
	// never persisted: the same state root and socket can host a new process
	// after a crash, an upgrade, or a manual restart, so every cached sequence,
	// revision and token belongs to exactly one instance. See
	// docs/internals/concurrency.md for why this is not about concurrent servers.
	instanceID            string
	engine                Engine
	engineMu              sync.RWMutex
	engineFactory         func() (Engine, error)
	canRestart            bool
	appleResourceMu       sync.Mutex
	appleResource         AppleResourceClient
	appleResourceFactory  func() (AppleResourceClient, error)
	appleResourceReady    bool
	engineRestarting      bool
	engineStopped         bool
	engineStop            chan struct{}
	engineStopOnce        sync.Once
	audioEngine           AudioEngine
	audioEngineFactory    func() (AudioEngine, error)
	audioCanRestart       bool
	audioEngineRestarting bool
	recent                *recentTracker
	activity              *activity.DB
	activityPath          string
	queuePacing           time.Duration
	store                 *state.Store
	radio                 *radio.Client
	radioCache            *radio.Cache
	radioCacheMu          sync.Mutex
	icy                   *icy.Client
	logf                  func(kind string, fields map[string]any)
	debugf                func(kind string, fields map[string]any)

	icyMu         sync.Mutex
	icyTitle      string
	icyArtist     string
	icyCancel     context.CancelFunc
	icyGeneration uint64

	listener *net.UnixListener
	lock     *fileLock

	mu                 sync.Mutex // serializes command execution
	draining           atomic.Bool
	sequence           uint64
	queueRevision      uint64
	queueUndo          *queueUndoReceipt
	queueUndoWindow    time.Duration
	now                func() time.Time
	stateRevision      uint64
	activeSource       api.SourceID
	activeTransport    TransportID
	playbackGeneration uint64
	transportSessionID string
	// switchSettleUntil suppresses stale notifications from the previous
	// provider for a short window after a source switch.
	switchSettleUntil time.Time
	// lastPublished dedups the snapshot-shaped watch events (sources.changed,
	// authorization.changed): the event name maps to the SHA-256 of the wire
	// data last published for it. A snapshot identical to the last published
	// one is neither republished nor given a sequence number, and every
	// publish path (warm-up, authorization flow, helper settle, provider
	// signature, engine lifecycle) shares this single gate, so no parallel
	// "last signature" can go stale. The first publish for an event always
	// goes out; the initial watch snapshot deliberately does not seed the
	// gate, or it would swallow that first correction. Occurrence-shaped
	// events never pass through it.
	lastPublished map[string][32]byte
	// providerSignatures tracks the last observed AvailabilitySignature of
	// providers whose availability can settle inside ordinary provider calls
	// (the Apple web provider learns its Widevine probe only once a browser
	// has started). The poll treats a moved signature as the trigger to
	// recompute descriptors; the sources.changed content gate makes the final
	// publish decision.
	providerSignatures map[api.SourceID]string
	urlTransport       *URLQueueTransport
	externalURLDriver  bool
	// urlStallBudget bounds how long a URL session may report buffering (or
	// report playing without advancing) before the server treats it as the media
	// failure the helper never reported. Injectable so tests can shorten it.
	urlStallBudget time.Duration
	// urlTransitionBudget overrides the playback.next execution budget for
	// automatic advances/retries in tests. Zero uses the command catalog.
	urlTransitionBudget time.Duration

	authFlows *flowManager

	authProviders map[api.SourceID]AuthProvider
	secureStore   securestore.Store
	providers     map[api.SourceID]ContentProvider

	watchers *watchHub

	closed       chan struct{}
	shutdown     chan struct{}
	shutdownOnce sync.Once
	// shutdownCloseOnce guards the single close of the serve-loop signal;
	// shutdownPublished guards the single server.shuttingDown event.
	shutdownCloseOnce sync.Once
	shutdownPublished bool
}

type queueUndoReceipt struct {
	token        string
	expiresAt    time.Time
	postRevision uint64
	source       api.SourceID
	transport    TransportID
	generation   uint64
	sessionID    string
	urlUndo      *URLQueueUndo
	engineHandle string
}

// Start acquires the lifecycle lock, binds the socket, and begins serving.
func Start(options Options) (*Server, error) {
	if options.SocketPath == "" {
		return nil, errors.New("server: SocketPath is required")
	}
	lockPath := options.LockPath
	if lockPath == "" {
		lockPath = filepath.Join(filepath.Dir(options.SocketPath), "server.lock")
	}
	lock, err := acquireLock(lockPath)
	if err != nil {
		if errors.Is(err, ErrActive) {
			return nil, ErrActive
		}
		return nil, err
	}
	listener, err := listenSocket(options.SocketPath)
	if err != nil {
		_ = lock.release()
		return nil, err
	}
	logf := options.Log
	if logf == nil {
		logf = func(string, map[string]any) {}
	}
	debugf := options.DebugLog
	if debugf == nil {
		debugf = func(string, map[string]any) {}
	}
	engine := options.Engine
	engineFactory := options.EngineFactory
	canRestart := engineFactory != nil
	if engineFactory == nil {
		engineFactory = func() (Engine, error) { return options.Engine, nil }
	}
	if canRestart {
		built, buildErr := engineFactory()
		if buildErr != nil {
			_ = listener.Close()
			_ = os.Remove(options.SocketPath)
			_ = lock.release()
			return nil, fmt.Errorf("start playback engine: %w", buildErr)
		}
		engine = built
	}
	activityPath := options.ActivityPath
	if activityPath == "" {
		activityPath = filepath.Join(filepath.Dir(options.SocketPath), "activity.sqlite3")
	}
	server := &Server{
		path:                 options.SocketPath,
		instanceID:           newServerInstanceID(),
		registry:             api.NewRegistry(),
		dedup:                newDedupCache(options.DedupBodies, options.DedupEntries, options.DedupTombstone),
		admission:            newAdmissionGate(),
		admissionWait:        options.AdmissionWait,
		engine:               engine,
		engineFactory:        engineFactory,
		canRestart:           canRestart,
		appleResourceFactory: options.AppleResourceFactory,
		engineStop:           make(chan struct{}),
		audioEngine:          options.AudioEngine,
		audioEngineFactory:   options.AudioEngineFactory,
		audioCanRestart:      options.AudioEngineFactory != nil,
		activity:             openActivity(activityPath, logf),
		activityPath:         activityPath,
		queuePacing:          queuePacing(options.QueuePacing),
		store:                options.Store,
		radio:                options.Radio,
		radioCache:           options.RadioCache,
		icy:                  options.ICY,
		logf:                 logf,
		debugf:               debugf,
		listener:             listener,
		lock:                 lock,
		watchers:             newWatchHub(),
		authFlows:            newFlowManager(),
		closed:               make(chan struct{}),
		shutdown:             make(chan struct{}),
		externalURLDriver:    options.URLPlaybackDriver != nil,
		queueUndoWindow:      options.QueueUndoWindow,
		now:                  options.Now,
	}
	if server.queueUndoWindow <= 0 {
		server.queueUndoWindow = 5 * time.Second
	}
	if server.now == nil {
		server.now = time.Now
	}
	// Deterministic tests may pass one combined engine for both roles, but only
	// when no dedicated audio-helper factory is configured; otherwise an Engine
	// that also satisfies AudioEngine must not shadow lilt-audio.
	if server.audioEngine == nil && options.AudioEngineFactory == nil && !canRestart {
		if audio, ok := engine.(AudioEngine); ok {
			server.audioEngine = audio
		}
	}
	if server.store != nil {
		server.activeSource = api.SourceID(server.store.LastPlaybackSource)
	}
	server.bindHandlers()
	server.setEngine(engine)
	server.secureStore = options.SecureStore
	if server.secureStore == nil {
		server.secureStore = securestore.NewMemory()
	}
	server.authProviders = server.buildAuthProviders(options.AuthProviders)
	server.providers = server.buildProviders(options.Providers, options.AudiusClient, options.JamendoClient)
	driver := options.URLPlaybackDriver
	if driver == nil {
		if candidate, ok := options.AudioEngine.(URLPlaybackDriver); ok {
			driver = candidate
		}
	}
	if driver == nil && options.AudioEngineFactory == nil && !canRestart {
		if candidate, ok := engine.(URLPlaybackDriver); ok {
			driver = candidate
		}
	}
	if driver != nil {
		server.urlTransport = NewURLQueueTransport(driver)
	}
	if server.store != nil {
		server.recent = newRecentTracker(options.RecentMin, server.recordRecentLocked)
	}
	server.startEngineWatch()
	server.warmUpAuthProviders()
	if server.startProviderSignatureWatch() {
		go server.watchProviderSignatures()
	}
	go server.accept()
	go server.runRecentSampler()
	go server.runURLStallWatchdog()
	return server, nil
}

func (s *Server) buildProviders(extra []ContentProvider, audiusClient *audius.Client, jamendoClient *jamendo.Client) map[api.SourceID]ContentProvider {
	audiusAPI := audius.Client{}
	if audiusClient != nil {
		audiusAPI = *audiusClient
	}
	jamendoAPI := jamendo.Client{}
	if jamendoClient != nil {
		jamendoAPI = *jamendoClient
	}
	if jamendoAPI.Credentials == nil {
		jamendoAPI.Credentials = func() (string, error) { return jamendo.LoadClientID(s.secureStore) }
	}
	providers := map[api.SourceID]ContentProvider{
		api.SourceAppleMusic: appleProvider{server: s},
		api.SourceAudius:     audiusProvider{client: audiusAPI, credentials: s.audiusCredentials},
		api.SourceJamendo:    jamendoProvider{client: jamendoAPI, credentials: jamendoAPI.Credentials},
	}
	for _, provider := range extra {
		if provider != nil {
			providers[provider.Source()] = provider
		}
	}
	return providers
}

// audiusCredentials reports the linked Audius account, gating the account-only
// library capability.
func (s *Server) audiusCredentials() (string, string, bool) {
	provider, ok := s.authProviders[api.SourceAudius].(*audiusAuthProvider)
	if !ok || provider == nil {
		return "", "", false
	}
	return provider.authorizationCredentials()
}

// currentEngine returns the active engine. It uses engineMu rather than s.mu so
// providers can read the engine while a command holds the command lock.
func (s *Server) currentEngine() Engine {
	s.engineMu.RLock()
	defer s.engineMu.RUnlock()
	return s.engine
}

// setEngine swaps the active engine. Callers hold s.mu; engineMu makes the
// pointer safe for readers that do not hold the command lock.
func (s *Server) setEngine(engine Engine) {
	s.engineMu.Lock()
	s.engine = engine
	s.engineMu.Unlock()
}

// ensureMusicEngineLocked and ensureAudioEngineLocked start only the helper
// required by the selected transport. Callers hold s.mu.
func (s *Server) ensureMusicEngineLocked() *api.Error {
	if s.engine != nil && !s.engineRestarting {
		return nil
	}
	if s.engineRestarting {
		return api.Errorf(api.CodeEngineRestarting, "the MusicKit helper is restarting; retry shortly")
	}
	engine, err := s.engineFactory()
	if err != nil || engine == nil {
		return api.Errorf(api.CodeSourceUnavailable, "music playback is unavailable")
	}
	s.setEngine(engine)
	s.watchEngine(engine)
	// The first lazy start is an availability change: watchers built their
	// descriptor snapshot while the helper was down, so republish sources.
	s.publishSourcesChangedLocked()
	return nil
}

func (s *Server) ensureAudioEngineLocked() *api.Error {
	if s.audioEngine != nil && !s.audioEngineRestarting {
		return nil
	}
	if s.audioEngineRestarting {
		return api.Errorf(api.CodeEngineRestarting, "the audio helper is restarting; retry shortly")
	}
	if s.audioEngineFactory == nil {
		return api.Errorf(api.CodeSourceUnavailable, "stream playback is unavailable")
	}
	engine, err := s.audioEngineFactory()
	if err != nil || engine == nil {
		return api.Errorf(api.CodeSourceUnavailable, "stream playback is unavailable")
	}
	s.audioEngine = engine
	if driver, ok := engine.(URLPlaybackDriver); ok {
		if s.urlTransport == nil {
			s.urlTransport = NewURLQueueTransport(driver)
		} else {
			s.urlTransport.SetDriver(driver)
		}
	}
	s.watchAudioEngine(engine)
	// First lazy start is an availability change; republish sources like the
	// restart path does so watch clients refresh capability snapshots.
	s.publishSourcesChangedLocked()
	return nil
}

// buildAuthProviders registers the built-in providers and applies overrides.
func (s *Server) buildAuthProviders(extra []AuthProvider) map[api.SourceID]AuthProvider {
	providers := map[api.SourceID]AuthProvider{
		api.SourceAppleMusic: newAppleAuthProvider(s),
	}
	providers[api.SourceRadio] = radioAuthProvider{}
	apiKey, redirectURI, scope := audiusOAuthConfig()
	providers[api.SourceAudius] = newAudiusAuthProvider(audius.Client{}, s.secureStore, apiKey, redirectURI, scope)
	providers[api.SourceJamendo] = newJamendoAuthProvider(s.secureStore)
	for _, provider := range extra {
		if provider != nil {
			providers[provider.Source()] = provider
		}
	}
	return providers
}

// warmUpAuthProviders starts the sessions of providers that declare they need
// one. It never blocks serve startup, and it republishes the settled state so
// a client that read the state too early is corrected instead of being left with a
// stale "unverified". The publish goes through the same content gate as every
// other snapshot event: the first warm-up publish always goes out (that is the
// correction), while a later publish of an identical snapshot is suppressed.
func (s *Server) warmUpAuthProviders() {
	for _, provider := range s.authProviders {
		warmup, ok := provider.(AuthWarmup)
		if !ok {
			continue
		}
		go func(source api.SourceID, warmup AuthWarmup) {
			warmup.WarmUp(context.Background())
			s.publishAuthorizationChange(source, "")
		}(provider.Source(), warmup)
	}
}

// ShutdownRequested is closed once a client asks the server to stop.
func (s *Server) ShutdownRequested() <-chan struct{} { return s.shutdown }

// prepareShutdown runs the ordered shutdown prologue from
// docs/client-api/protocol.md §6: it linearizes draining (new side-effecting
// commands answer session_unavailable), stops audio and pending authorization
// flows, then publishes server.shuttingDown exactly once. It deliberately does
// NOT close the listener/watch/helper or signal the serve loop; the caller must
// write its reply first and then call finishShutdown.
func (s *Server) prepareShutdown(ctx context.Context) {
	s.shutdownOnce.Do(func() {
		// dispatch already took the barrier when the shutdown handler succeeded;
		// storing it again is idempotent and covers a shutdown that reached its
		// response through the ledger instead of a fresh execution.
		s.draining.Store(true)
		s.stopEngineSupervisor()
		s.authFlows.cancelAll()
		s.releasePlayback(ctx)
		s.publishShutdownOnce()
	})
}

// finishShutdown wakes the serve loop after the shutdown reply has been written,
// so the listener/helper teardown cannot truncate the caller's response.
func (s *Server) finishShutdown() {
	s.shutdownCloseOnce.Do(func() { close(s.shutdown) })
}

// triggerShutdown is the composed form used by tests and signal paths: prepare
// then signal.
func (s *Server) triggerShutdown() {
	s.prepareShutdown(context.Background())
	s.finishShutdown()
}

// publishShutdownOnce publishes server.shuttingDown at most once per process, so
// a client-triggered shutdown and the later Close do not emit the event twice.
func (s *Server) publishShutdownOnce() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdownPublished {
		return
	}
	s.shutdownPublished = true
	s.sequence++
	s.publishLocked("server.shuttingDown", map[string]any{})
}

// releasePlayback stops audio and releases the playback resources. It is
// idempotent: the engine/apple-resource fields are cleared under the lock, so a
// later Close cannot double-close them.
func (s *Server) releasePlayback(ctx context.Context) {
	s.mu.Lock()
	engine := s.engine
	audioEngine := s.audioEngine
	s.setEngine(nil)
	s.audioEngine = nil
	s.appleResourceMu.Lock()
	appleResource := s.appleResource
	s.appleResource = nil
	s.appleResourceMu.Unlock()
	s.mu.Unlock()

	s.stopICY()
	if engine != nil {
		_ = engine.UnsubscribeState(ctx)
		if closer, ok := engine.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}
	if audioEngine != nil {
		_ = audioEngine.UnsubscribeState(ctx)
		if closer, ok := audioEngine.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}
	if closer, ok := appleResource.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

// stopEngineSupervisor marks the engine stopped (so no rebuild starts) and
// releases any goroutine waiting to retry.
func (s *Server) stopEngineSupervisor() {
	s.mu.Lock()
	s.engineStopped = true
	s.mu.Unlock()
	s.engineStopOnce.Do(func() { close(s.engineStop) })
}

// Close stops accepting connections, releases the playback engine (stopping
// playback and terminating the helper), removes the socket, and releases the
// lock.
func (s *Server) Close() error {
	select {
	case <-s.closed:
		return nil
	default:
		close(s.closed)
	}
	s.draining.Store(true)
	s.stopEngineSupervisor()
	s.releasePlayback(context.Background())
	s.authFlows.cancelAll()
	s.publishShutdownOnce()
	s.watchers.closeAll()
	listenerErr := s.listener.Close()
	removeErr := os.Remove(s.path)
	if s.activity != nil {
		_ = s.activity.Close()
		s.activity = nil
	}
	if removeErr != nil && !os.IsNotExist(removeErr) {
		_ = s.lock.release()
		return removeErr
	}
	_ = s.lock.release()
	return listenerErr
}

func (s *Server) accept() {
	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			select {
			case <-s.closed:
				return
			default:
				continue
			}
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn *net.UnixConn) {
	defer conn.Close()
	// Bound the initial request read so a client cannot hold a connection
	// indefinitely without sending a complete request.
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var request api.Request
	if err := json.NewDecoder(conn).Decode(&request); err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if request.Command == "session.watch" {
		// Watch keeps a long-lived connection, so it bypasses dispatch. It must
		// still honor the wire rules dispatch applies: a non-empty requestId and
		// the closed params schema (an unknown key would otherwise be silently
		// ignored and subscribe the client to all topics).
		if request.RequestID == "" {
			_ = json.NewEncoder(conn).Encode(s.stampInstance(s.fail("", api.Errorf(api.CodeInvalidRequest, "requestId is required"))))
			return
		}
		if _, validationErr := s.registry.ValidateParams(request.Command, request.Params); validationErr != nil {
			_ = json.NewEncoder(conn).Encode(s.stampInstance(s.fail(request.RequestID, validationErr)))
			return
		}
		if epochErr := s.checkServerInstance(request); epochErr != nil {
			_ = json.NewEncoder(conn).Encode(s.stampInstance(s.fail(request.RequestID, epochErr)))
			return
		}
		s.serveWatch(conn, request)
		return
	}
	start := time.Now()
	// An unregistered command has no side effects to protect, so it reports the
	// stable unknown_command whether or not the caller sent an epoch. For known
	// commands the epoch precondition is still a wire rule checked before
	// dispatch, so a rejected request never registers in the dedup ledger or
	// reaches a handler.
	var response api.Response
	if _, known := s.registry.Lookup(request.Command); request.Command != "" && !known {
		response = s.fail(request.RequestID, api.Errorf(api.CodeUnknownCommand, "unknown command %q", request.Command))
	} else if epochErr := s.checkServerInstance(request); epochErr != nil {
		response = s.fail(request.RequestID, epochErr)
	} else {
		response = s.guardedDispatch(request)
	}
	response = s.stampInstance(response)
	s.logRequest(request, response, time.Since(start))
	// Only an accepted shutdown may run the protocol.md §6 prologue. Invalid
	// requests and failed/deduplicated error responses must not stop playback.
	// Drain and publish before replying, then signal teardown after the reply
	// so the listener/helper cannot truncate it.
	shutdown := request.Command == "session.shutdown" && response.OK
	if shutdown {
		s.prepareShutdown(context.Background())
	}
	_ = json.NewEncoder(conn).Encode(response)
	if shutdown {
		s.finishShutdown()
	}
}

// logRequest records one Client API command on the debug channel so a session
// can be reconstructed: requestId joins it to the client log, and command/ok/
// errorCode/ms describe the outcome. Params are written in full only at debug
// level (the journal still redacts credential-shaped fields).
func (s *Server) logRequest(request api.Request, response api.Response, elapsed time.Duration) {
	fields := map[string]any{
		"requestId": request.RequestID,
		"command":   request.Command,
		"ok":        response.OK,
		"ms":        elapsed.Milliseconds(),
	}
	if response.Error != nil {
		fields["errorCode"] = response.Error.Code
	}
	if len(request.Params) > 0 {
		fields["params"] = json.RawMessage(request.Params)
	}
	if len(response.Data) > 0 {
		fields["resultBytes"] = len(response.Data)
	}
	if s.debugf != nil {
		s.debugf("server.request", fields)
	}
}

// guardedDispatch runs one client command under a panic guard: a handler or
// provider panic lands in the journal with a full stack and the client gets a
// stable internal_error response, while the server — and every other client —
// keeps working. The trace is the debugging entry point; the response only
// says what happened, not the raw panic.
func (s *Server) guardedDispatch(request api.Request) (response api.Response) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("server.panic", map[string]any{
				"command": request.Command,
				"panic":   fmt.Sprint(r),
				"stack":   string(debug.Stack()),
			})
			response = s.fail(request.RequestID, api.Errorf(api.CodeInternalError, "the command panicked; its trace is in the journal"))
		}
	}()
	return s.dispatch(request)
}

func (s *Server) dispatch(request api.Request) api.Response {
	if request.Command == "" {
		return s.fail(request.RequestID, api.Errorf(api.CodeInvalidRequest, "command is required"))
	}
	if request.RequestID == "" {
		return s.fail("", api.Errorf(api.CodeInvalidRequest, "requestId is required"))
	}
	// draining is read without the state lock: admission must be the first
	// thing a queued mutation blocks on (and it is bounded), not a lock held for
	// the whole duration of another handler's I/O.
	if s.draining.Load() && !s.registry.ServeWhileDraining(request.Command) {
		return s.fail(request.RequestID, api.Errorf(api.CodeSessionUnavailable, "the server is shutting down"))
	}
	params, validationErr := s.registry.ValidateParams(request.Command, request.Params)
	if validationErr != nil {
		return s.fail(request.RequestID, validationErr)
	}
	fingerprint := fingerprint(request.Command, params)

	// The dedup guarantee covers commands with side effects. A pure query keeps
	// in-flight sharing while the ledger has room, and degrades to running
	// without caching when it does not, so state verification is never blocked
	// by an unrelated saturated ledger.
	deduped := true
	var entry *dedupEntry
	var isNew bool
	var dedupErr *api.Error
	if s.registry.Query(request.Command) {
		entry, isNew, deduped, dedupErr = s.dedup.beginQuery(request.RequestID, fingerprint)
	} else {
		entry, isNew, dedupErr = s.dedup.begin(request.RequestID, fingerprint)
	}
	if dedupErr != nil {
		return s.fail(request.RequestID, dedupErr)
	}
	if deduped && !isNew {
		select {
		case <-entry.done:
			return s.withRequestID(s.dedup.result(entry), request.RequestID)
		case <-s.closed:
			return s.fail(request.RequestID, api.Errorf(api.CodeSessionUnavailable, "server is closing"))
		}
	}
	finish := func(response api.Response) api.Response {
		if deduped {
			s.dedup.finish(request.RequestID, response)
		}
		return response
	}

	handler, handlerErr := s.registry.Handler(request.Command)
	if handlerErr != nil {
		return finish(s.fail(request.RequestID, handlerErr))
	}

	timeout := s.registry.Timeout(request.Command)
	execute := func() (any, *api.Error) {
		ctx := context.Background()
		if timeout <= 0 {
			return handler(ctx, params)
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return handler(ctx, params)
	}
	var data any
	var executeErr *api.Error
	if s.registry.Concurrent(request.Command) {
		// Recover here rather than only at the connection boundary: the
		// dedup entry must be completed so same-request retries can return.
		func() {
			defer func() {
				if r := recover(); r != nil {
					s.logf("server.panic", map[string]any{
						"command": request.Command,
						"panic":   fmt.Sprint(r),
						"stack":   string(debug.Stack()),
					})
					executeErr = api.Errorf(api.CodeInternalError, "the command panicked; its trace is in the journal")
				}
			}()
			data, executeErr = execute()
		}()
	} else {
		// Admission is the FIFO linearization point for public side effects: a
		// request that is admitted here has a fixed order relative to other
		// admitted requests, which a mutex alone does not provide. A request that
		// cannot be admitted inside its budget certainly did not run, so it is
		// reported as server_busy and its pending ledger entry is dropped: a
		// transient rejection must not be cached as an outcome.
		// Only a command that actually took the slot releases it: a pure query and
		// an over-budget request must never free a slot owned by another command.
		if wait := s.admissionWaitFor(request.Command); wait > 0 {
			if !s.admission.acquire(wait, s.closed) {
				busy := s.fail(request.RequestID, api.Errorf(api.CodeServerBusy,
					"%s waited %s for the server's mutation slot and did not run; retry or read state first", request.Command, wait))
				if deduped {
					s.dedup.abort(request.RequestID, busy)
				}
				return busy
			}
			defer s.admission.release()
		}
		// Re-check draining now that this request owns the slot: the barrier is
		// taken when shutdown is accepted, so a request that queued before it must
		// not execute after it.
		if s.draining.Load() && !s.registry.ServeWhileDraining(request.Command) {
			busy := s.fail(request.RequestID, api.Errorf(api.CodeSessionUnavailable, "the server is shutting down"))
			if deduped {
				s.dedup.abort(request.RequestID, busy)
			}
			return busy
		}
		// Queue wait is not command execution time. Start the command budget only
		// after this request owns the serialized mutation slot; otherwise a short
		// control command can expire before its handler starts.
		s.mu.Lock()
		// A handler panic must not leave the serialized slot locked or the
		// dedup entry pending: the guard recovers the lock, journals the panic
		// with its stack, and reports a stable internal_error.
		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					s.mu.Unlock()
					s.logf("server.panic", map[string]any{
						"command": request.Command,
						"panic":   fmt.Sprint(r),
						"stack":   string(debug.Stack()),
					})
					executeErr = api.Errorf(api.CodeInternalError, "the command panicked; its trace is in the journal")
				}
			}()
			data, executeErr = execute()
		}()
		if !panicked {
			s.mu.Unlock()
		}
		// The shutdown barrier is taken here, while this request still owns the
		// mutation slot and only after its handler succeeded: requests already
		// queued ahead of it run, and everything handed the slot afterwards is
		// rejected. Taking it in prepareShutdown (after the slot was released)
		// left a window where a woken request saw draining=false.
		if request.Command == "session.shutdown" && executeErr == nil {
			s.draining.Store(true)
		}
	}

	var response api.Response
	if executeErr != nil {
		response = s.fail(request.RequestID, executeErr)
	} else {
		response = api.Success(request.RequestID, data)
	}
	return finish(response)
}

// newServerInstanceID returns an opaque epoch for one server process. It is
// random per start because it only needs to be distinguishable from the epoch
// of any earlier or later process on the same state root, not globally unique.
func newServerInstanceID() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("epoch-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

// stampInstance puts the epoch on every response, including failures, so a
// client always learns the process behind the socket without an extra query.
func (s *Server) stampInstance(response api.Response) api.Response {
	response.ServerInstanceID = s.instanceID
	return response
}

// admissionWaitFor returns how long a command may wait for the mutation slot.
// The catalog is the single source of the budget; AdmissionWait only shortens it
// for hermetic tests that cannot spend the real 5s or the derived shutdown wait.
// Pure queries return zero: they never take the slot.
func (s *Server) admissionWaitFor(command string) time.Duration {
	wait := s.registry.Admission(command)
	if s.admissionWait > 0 && wait > 0 && wait == api.DefaultAdmission() {
		return s.admissionWait
	}
	return wait
}

// checkServerInstance enforces the wire epoch precondition at the transport
// boundary: a side-effecting command must state which server instance it was
// composed against, and a pure query may omit it. A mismatch is conflict, never
// a silent re-target: the caller re-reads state and decides again
// (docs/internals/concurrency.md §5.2). dispatch() itself stays epoch-free so
// in-process callers and unit tests exercise handlers without a wire envelope.
func (s *Server) checkServerInstance(request api.Request) *api.Error {
	if request.Command == "" || s.registry.Query(request.Command) {
		if request.IfServerInstanceID == "" || request.IfServerInstanceID == s.instanceID {
			return nil
		}
		return epochConflict(s.instanceID)
	}
	if request.IfServerInstanceID == "" {
		return api.Errorf(api.CodeInvalidRequest, "ifServerInstanceId is required for %s", request.Command)
	}
	if request.IfServerInstanceID != s.instanceID {
		return epochConflict(s.instanceID)
	}
	return nil
}

func epochConflict(instanceID string) *api.Error {
	return api.Errorf(api.CodeConflict, "the server instance changed since this client read its state; re-read and retry").
		WithDetails(map[string]any{"reason": "server_epoch", "serverInstanceId": instanceID})
}

func (s *Server) fail(requestID string, err *api.Error) api.Response {
	return api.Failure(requestID, err)
}

// defaultQueuePacing is the conservative gap between MusicKit appends; see
// startEngineQueueLocked for why a gap exists at all.
const defaultQueuePacing = 700 * time.Millisecond

func queuePacing(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return defaultQueuePacing
}

func (s *Server) withRequestID(response api.Response, requestID string) api.Response {
	response.RequestID = requestID
	return response
}

func listenSocket(path string) (*net.UnixListener, error) {
	dir := filepath.Dir(path)
	_, statErr := os.Stat(dir)
	createdDir := os.IsNotExist(statErr)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	// Only tighten permissions on a directory lilt just created. Explicit
	// LILT_SOCKET paths may live in a shared directory (for example /tmp) that
	// must not be chmod-ed.
	if createdDir {
		_ = os.Chmod(dir, 0700)
	}
	if _, err := os.Lstat(path); err == nil {
		conn, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return nil, ErrActive
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale session socket: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return listener, nil
}
