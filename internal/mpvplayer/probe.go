package mpvplayer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/caiguo/lilt/core"
)

// Probe checks whether a stream is actually delivering audio. macOS asks
// AVFoundation to load the stream; on Linux the equivalent is an HTTP GET that
// waits for the first audio bytes, so both platforms answer the same question
// with the same error codes (unsupported / http / timeout / network).
func (c *Client) Probe(ctx context.Context, streamURL string, timeoutMs int) (core.RadioProbeResult, error) {
	parsed, err := url.Parse(streamURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return core.RadioProbeResult{
			Status:    "failed",
			ErrorCode: "unsupported",
			Message:   "only http and https streams can be probed",
		}, nil
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 || timeout > 15*time.Second {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return core.RadioProbeResult{Status: "failed", ErrorCode: "unsupported", Message: err.Error()}, nil
	}
	request.Header.Set("Icy-MetaData", "1")
	request.Header.Set("User-Agent", "lilt/1")

	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return core.RadioProbeResult{Status: "failed", ErrorCode: probeErrorCode(err), Message: err.Error()}, nil
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return core.RadioProbeResult{
			Status:    "failed",
			ErrorCode: "http",
			Message:   fmt.Sprintf("HTTP status %d", response.StatusCode),
		}, nil
	}
	// Healthy means the stream delivers audio, not merely that it answered: a
	// station that accepts the connection and then stalls is down. The body is
	// closed on return, which aborts the rest of the stream.
	buffer := make([]byte, 4096)
	for {
		read, readErr := response.Body.Read(buffer)
		if read > 0 {
			return core.RadioProbeResult{Status: "healthy", LatencyMs: int(time.Since(started).Milliseconds())}, nil
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return core.RadioProbeResult{
					Status:    "failed",
					ErrorCode: "network",
					Message:   "stream closed before sending audio",
				}, nil
			}
			return core.RadioProbeResult{Status: "failed", ErrorCode: probeErrorCode(readErr), Message: readErr.Error()}, nil
		}
	}
}

// probeErrorCode classifies a transport failure using the same vocabulary as
// the macOS probe: a deadline is "timeout", everything else is "network".
func probeErrorCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var networkErr interface{ Timeout() bool }
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		return "timeout"
	}
	if strings.Contains(err.Error(), "context deadline exceeded") {
		return "timeout"
	}
	return "network"
}
