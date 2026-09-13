// Package session hosts the private control socket owned by a foreground TUI.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/caiguo/lilt/core"
	"github.com/caiguo/lilt/internal/player"
	"github.com/caiguo/lilt/internal/protocol"
)

var ErrActive = errors.New("an active lilt session already owns this socket")

type Server struct {
	path     string
	target   core.PlaybackTarget
	listener *net.UnixListener
	mu       sync.Mutex
	closed   chan struct{}
}

func Start(path string, target core.PlaybackTarget) (*Server, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	_ = os.Chmod(filepath.Dir(path), 0700)
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
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = l.Close()
		_ = os.Remove(path)
		return nil, err
	}
	s := &Server{path: path, target: target, listener: l, closed: make(chan struct{})}
	go s.accept()
	return s, nil
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
	var req protocol.SessionRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var result any
	var err error
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch req.Command {
	case "status":
		result, err = s.target.State(ctx)
	case "play":
		request, parseErr := core.ParseReference(req.Reference)
		if parseErr != nil {
			_ = json.NewEncoder(conn).Encode(protocol.Failure("invalid_reference", parseErr.Error()))
			return
		}
		err = s.target.Play(ctx, request)
	case "pause":
		err = s.target.Pause(ctx)
	case "resume":
		err = s.target.Resume(ctx)
	case "next":
		err = s.target.Next(ctx)
	case "previous":
		err = s.target.Previous(ctx)
	default:
		_ = json.NewEncoder(conn).Encode(protocol.Failure("unknown_command", "unknown session command"))
		return
	}
	if err != nil {
		code := "playback_error"
		var rpcErr *player.RPCError
		if errors.As(err, &rpcErr) {
			code = rpcErr.Code
		}
		_ = json.NewEncoder(conn).Encode(protocol.Failure(code, err.Error()))
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Success(result))
}

func (s *Server) Close() error {
	select {
	case <-s.closed:
		return nil
	default:
		close(s.closed)
	}
	err := s.listener.Close()
	removeErr := os.Remove(s.path)
	if removeErr != nil && !os.IsNotExist(removeErr) {
		return removeErr
	}
	return err
}

func SocketPath() string {
	if value := os.Getenv("LILT_SOCKET"); value != "" {
		return value
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), fmt.Sprintf("lilt-%d.sock", os.Getuid()))
	}
	return filepath.Join(dir, "lilt", "session.sock")
}

func Call(ctx context.Context, path, command string) (protocol.Envelope, error) {
	return CallRequest(ctx, path, protocol.SessionRequest{Command: command})
}

func CallRequest(ctx context.Context, path string, request protocol.SessionRequest) (protocol.Envelope, error) {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file") || strings.Contains(err.Error(), "connection refused") {
			return protocol.Failure("no_active_session", "no active lilt TUI session"), nil
		}
		return protocol.Failure("session_unavailable", err.Error()), nil
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return protocol.Failure("session_unavailable", err.Error()), nil
	}
	var response protocol.Envelope
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return protocol.Failure("session_unavailable", err.Error()), nil
	}
	return response, nil
}
