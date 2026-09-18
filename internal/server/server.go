// Package server hosts the single lilt server: it owns the playback engine,
// the authoritative state, the source registry, and the Client API socket.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/audius"
	"github.com/caiguo/lilt/internal/icy"
	"github.com/caiguo/lilt/internal/radio"
	"github.com/caiguo/lilt/internal/securestore"
	"github.com/caiguo/lilt/internal/state"
)

// Options configures a Server. Only SocketPath is required; tests supply a fake
// Engine and an isolated Store/RadioCache.
type Options struct {
	SocketPath string
	LockPath   string
	Engine     Engine
	// EngineFactory, when set, builds a fresh engine and enables automatic
	// rebuild after a helper transport failure. Engine is used only when no
	// factory is given.
	EngineFactory func() (Engine, error)
	// AudioEngineFactory starts lilt-audio on demand and rebuilds it after a
	// transport failure. AudioEngine is the deterministic-test alternative.
	AudioEngine        AudioEngine
	AudioEngineFactory func() (AudioEngine, error)
	Store              *state.Store
	Radio              *radio.Client
	RadioCache         *radio.Cache
	ICY                *icy.Client
	// AuthProviders add or override authorization providers by source. Apple
	// and radio are registered automatically; tests pass a scriptable fixture.
	AuthProviders []AuthProvider
	// Providers replace or extend compiled-in content providers. This keeps
	// HTTP discovery hermetic in socket tests.
	Providers    []ContentProvider
	AudiusClient *audius.Client
	// SecureStore holds provider credentials. Tests default to an in-memory
	// store; production passes the platform Keychain.
	SecureStore       securestore.Store
	URLPlaybackDriver URLPlaybackDriver

	// RecentMin caps the recent-history threshold; tests set it small. Zero
	// means the documented 30s.
	RecentMin time.Duration

	DedupBodies    int
	DedupTombstone time.Duration

	Log func(kind string, fields map[string]any)
}

// Server is the single owner of playback, queue, and persisted state.
type Server struct {
	path     string
	registry *api.Registry
	dedup    *dedupCache

	engine                Engine
	engineMu              sync.RWMutex
	engineFactory         func() (Engine, error)
	canRestart            bool
	engineRestarting      bool
	engineStopped         bool
	engineStop            chan struct{}
	engineStopOnce        sync.Once
	audioEngine           AudioEngine
	audioEngineFactory    func() (AudioEngine, error)
	audioCanRestart       bool
	audioEngineRestarting bool
	recent                *recentTracker
	store                 *state.Store
	radio                 *radio.Client
	radioCache            *radio.Cache
	icy                   *icy.Client
	logf                  func(kind string, fields map[string]any)

	icyMu         sync.Mutex
	icyTitle      string
	icyArtist     string
	icyCancel     context.CancelFunc
	icyGeneration uint64

	listener *net.UnixListener
	lock     *fileLock

	mu                 sync.Mutex // serializes command execution
	sequence           uint64
	queueRevision      uint64
	stateRevision      uint64
	activeSource       api.SourceID
	activeTransport    TransportID
	playbackGeneration uint64
	transportSessionID string
	// switchSettleUntil suppresses stale notifications from the previous
	// provider for a short window after a source switch.
	switchSettleUntil time.Time
	urlTransport      *URLQueueTransport
	externalURLDriver bool
	draining          bool

	authFlows *flowManager

	authProviders map[api.SourceID]AuthProvider
	secureStore   securestore.Store
	providers     map[api.SourceID]ContentProvider

	watchers *watchHub

	closed       chan struct{}
	shutdown     chan struct{}
	shutdownOnce sync.Once
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
	server := &Server{
		path:               options.SocketPath,
		registry:           api.NewRegistry(),
		dedup:              newDedupCache(options.DedupBodies, options.DedupTombstone),
		engine:             engine,
		engineFactory:      engineFactory,
		canRestart:         canRestart,
		engineStop:         make(chan struct{}),
		audioEngine:        options.AudioEngine,
		audioEngineFactory: options.AudioEngineFactory,
		audioCanRestart:    options.AudioEngineFactory != nil,
		store:              options.Store,
		radio:              options.Radio,
		radioCache:         options.RadioCache,
		icy:                options.ICY,
		logf:               logf,
		listener:           listener,
		lock:               lock,
		watchers:           newWatchHub(),
		authFlows:          newFlowManager(),
		closed:             make(chan struct{}),
		shutdown:           make(chan struct{}),
		externalURLDriver:  options.URLPlaybackDriver != nil,
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
	server.providers = server.buildProviders(options.Providers, options.AudiusClient)
	driver := options.URLPlaybackDriver
	if driver == nil {
		if candidate, ok := options.AudioEngine.(URLPlaybackDriver); ok {
			driver = candidate
		}
	}
	if driver != nil {
		server.urlTransport = NewURLQueueTransport(driver)
	}
	if server.store != nil {
		server.recent = newRecentTracker(options.RecentMin, server.recordRecent)
	}
	server.startEngineWatch()
	go server.accept()
	go server.runRecentSampler()
	return server, nil
}

func (s *Server) buildProviders(extra []ContentProvider, audiusClient *audius.Client) map[api.SourceID]ContentProvider {
	client := audius.Client{}
	if audiusClient != nil {
		client = *audiusClient
	}
	providers := map[api.SourceID]ContentProvider{
		api.SourceAppleMusic: appleProvider{server: s},
		api.SourceAudius:     audiusProvider{client: client, credentials: s.audiusCredentials},
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
		return api.Errorf(api.CodeSourceUnavailable, "MusicKit playback is unavailable")
	}
	s.setEngine(engine)
	s.watchEngine(engine)
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
		return api.Errorf(api.CodeSourceUnavailable, "AVPlayer playback is unavailable")
	}
	engine, err := s.audioEngineFactory()
	if err != nil || engine == nil {
		return api.Errorf(api.CodeSourceUnavailable, "AVPlayer playback is unavailable")
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
	for _, provider := range extra {
		if provider != nil {
			providers[provider.Source()] = provider
		}
	}
	return providers
}

// ShutdownRequested is closed once a client asks the server to stop.
func (s *Server) ShutdownRequested() <-chan struct{} { return s.shutdown }

func (s *Server) triggerShutdown() {
	s.shutdownOnce.Do(func() {
		s.mu.Lock()
		s.draining = true
		s.mu.Unlock()
		s.stopEngineSupervisor()
		close(s.shutdown)
	})
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
	s.mu.Lock()
	s.draining = true
	engine := s.engine
	audioEngine := s.audioEngine
	s.setEngine(nil)
	s.audioEngine = nil
	s.sequence++
	s.publishLocked("server.shuttingDown", map[string]any{})
	s.mu.Unlock()

	s.stopEngineSupervisor()
	s.stopICY()
	s.authFlows.cancelAll()
	if engine != nil {
		_ = engine.UnsubscribeState(context.Background())
		if closer, ok := engine.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}
	if audioEngine != nil {
		_ = audioEngine.UnsubscribeState(context.Background())
		if closer, ok := audioEngine.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}
	s.watchers.closeAll()
	listenerErr := s.listener.Close()
	removeErr := os.Remove(s.path)
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
		s.serveWatch(conn, request)
		return
	}
	response := s.dispatch(request)
	_ = json.NewEncoder(conn).Encode(response)
	if request.Command == "session.shutdown" {
		s.triggerShutdown()
	}
}

func (s *Server) dispatch(request api.Request) api.Response {
	if request.Command == "" {
		return s.fail(request.RequestID, api.Errorf(api.CodeInvalidRequest, "command is required"))
	}
	if request.RequestID == "" {
		return s.fail("", api.Errorf(api.CodeInvalidRequest, "requestId is required"))
	}
	s.mu.Lock()
	draining := s.draining
	s.mu.Unlock()
	if draining && !readOnlyCommand(request.Command) {
		return s.fail(request.RequestID, api.Errorf(api.CodeSessionUnavailable, "the server is shutting down"))
	}
	params, validationErr := s.registry.ValidateParams(request.Command, request.Params)
	if validationErr != nil {
		return s.fail(request.RequestID, validationErr)
	}
	fingerprint := fingerprint(request.Command, params)

	entry, isNew, dedupErr := s.dedup.begin(request.RequestID, fingerprint)
	if dedupErr != nil {
		return s.fail(request.RequestID, dedupErr)
	}
	if !isNew {
		select {
		case <-entry.done:
			return s.withRequestID(s.dedup.result(entry), request.RequestID)
		case <-s.closed:
			return s.fail(request.RequestID, api.Errorf(api.CodeSessionUnavailable, "server is closing"))
		}
	}

	handler, handlerErr := s.registry.Handler(request.Command)
	if handlerErr != nil {
		response := s.fail(request.RequestID, handlerErr)
		s.dedup.finish(request.RequestID, response)
		return response
	}

	timeout := s.registry.Timeout(request.Command)
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	s.mu.Lock()
	data, executeErr := handler(ctx, params)
	s.mu.Unlock()

	var response api.Response
	if executeErr != nil {
		response = s.fail(request.RequestID, executeErr)
	} else {
		response = api.Success(request.RequestID, data)
	}
	s.dedup.finish(request.RequestID, response)
	return response
}

func (s *Server) fail(requestID string, err *api.Error) api.Response {
	return api.Failure(requestID, err)
}

// readOnlyCommand reports commands that may still be served while the server is
// draining for shutdown.
func readOnlyCommand(name string) bool {
	switch name {
	case "api.describe", "sources.list", "session.status", "session.shutdown",
		"authorization.list", "authorization.status", "discovery.search",
		"playlist.tracks", "library.playlists", "recent.list", "recommendations.list",
		"radio.search", "radio.options", "radio.probe", "state.get", "favorites.list":
		return true
	}
	return false
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
