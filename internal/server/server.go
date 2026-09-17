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
	"sync"
	"time"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/icy"
	"github.com/caiguo/lilt/internal/radio"
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
	Store         *state.Store
	Radio         *radio.Client
	RadioCache    *radio.Cache
	ICY           *icy.Client
	ServerID      string
	// AuthProviders add or override authorization providers by source. Apple
	// and radio are registered automatically; tests pass a scriptable fixture.
	AuthProviders []AuthProvider

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
	serverID string
	registry *api.Registry
	dedup    *dedupCache

	engine           Engine
	engineMu         sync.RWMutex
	engineFactory    func() (Engine, error)
	canRestart       bool
	engineRestarting bool
	engineStopped    bool
	engineStop       chan struct{}
	engineStopOnce   sync.Once
	recent           *recentTracker
	store            *state.Store
	radio            *radio.Client
	radioCache       *radio.Cache
	icy              *icy.Client
	logf             func(kind string, fields map[string]any)

	icyMu         sync.Mutex
	icyTitle      string
	icyArtist     string
	icyCancel     context.CancelFunc
	icyGeneration uint64

	listener *net.UnixListener
	lock     *fileLock

	mu            sync.Mutex // serializes command execution
	sequence      uint64
	queueRevision uint64
	stateRevision uint64
	draining      bool

	authFlows *flowManager

	authProviders map[api.SourceID]AuthProvider

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
	serverID := options.ServerID
	if serverID == "" {
		serverID = newServerID()
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
		path:          options.SocketPath,
		serverID:      serverID,
		registry:      api.NewRegistry(),
		dedup:         newDedupCache(options.DedupBodies, options.DedupTombstone),
		engine:        engine,
		engineFactory: engineFactory,
		canRestart:    canRestart,
		engineStop:    make(chan struct{}),
		store:         options.Store,
		radio:         options.Radio,
		radioCache:    options.RadioCache,
		icy:           options.ICY,
		logf:          logf,
		listener:      listener,
		lock:          lock,
		watchers:      newWatchHub(),
		authFlows:     newFlowManager(),
		closed:        make(chan struct{}),
		shutdown:      make(chan struct{}),
	}
	server.bindHandlers()
	server.setEngine(engine)
	server.authProviders = server.buildAuthProviders(options.AuthProviders)
	if server.store != nil {
		server.recent = newRecentTracker(options.RecentMin, server.recordRecent)
	}
	server.startEngineWatch()
	go server.accept()
	go server.runRecentSampler()
	return server, nil
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

// buildAuthProviders registers the built-in providers and applies overrides.
func (s *Server) buildAuthProviders(extra []AuthProvider) map[api.SourceID]AuthProvider {
	providers := map[api.SourceID]AuthProvider{}
	if s.currentEngine() != nil {
		providers[api.SourceAppleMusic] = newAppleAuthProvider(s)
	}
	providers[api.SourceRadio] = radioAuthProvider{}
	for _, provider := range extra {
		if provider != nil {
			providers[provider.Source()] = provider
		}
	}
	return providers
}

// ServerID returns the identity reported in every response.
func (s *Server) ServerID() string { return s.serverID }

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
	s.setEngine(nil)
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
	if request.Version != api.Version {
		return s.fail(request.RequestID, api.Errorf(api.CodeInvalidRequest, "unsupported api version %d", request.Version))
	}
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
			return s.withIdentity(s.dedup.result(entry), request.RequestID)
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
		response = api.Success(request.RequestID, s.serverID, data)
	}
	s.dedup.finish(request.RequestID, response)
	return response
}

func (s *Server) fail(requestID string, err *api.Error) api.Response {
	return api.Failure(requestID, s.serverID, err)
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

func (s *Server) withIdentity(response api.Response, requestID string) api.Response {
	response.RequestID = requestID
	response.ServerID = s.serverID
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

func newServerID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("server-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}
