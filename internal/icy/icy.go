// Package icy reads Shoutcast/Icecast inline stream metadata (the "ICY"
// StreamTitle) from an HTTP audio stream. It is used by the server to enrich
// live radio playback state with the currently announced artist and title.
//
// "ICY" is not a formal acronym: it comes from the non-standard "ICY 200 OK"
// status line and icy-* headers used by SHOUTcast/Icecast, and is generally
// understood as shorthand for Icecast. There is no official expansion.
package icy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// maxMetadataBytes bounds a single metadata block; the length prefix is a byte
// counting 16-byte units, so the on-wire maximum is 255*16.
const maxMetadataBytes = 255 * 16

// Update is one announced stream title.
type Update struct {
	Title  string
	Artist string
}

// Client reads ICY metadata from streams.
type Client struct {
	HTTP *http.Client
}

// New returns a client using net/http defaults.
func New() *Client { return &Client{} }

// Watch connects to streamURL with Icy-MetaData enabled and calls onUpdate for
// every changed StreamTitle until ctx is cancelled or the stream ends. A stream
// without an icy-metaint header is not an error: it simply has no inline
// metadata, so Watch returns nil immediately.
func (c *Client) Watch(ctx context.Context, streamURL string, onUpdate func(Update)) error {
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Icy-MetaData", "1")
	request.Header.Set("User-Agent", "lilt/1")
	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return fmt.Errorf("stream returned HTTP %d", response.StatusCode)
	}
	metaInterval := response.Header.Get("icy-metaint")
	if metaInterval == "" {
		return nil
	}
	interval, err := strconv.Atoi(metaInterval)
	if err != nil || interval <= 0 {
		return nil
	}
	reader := bufio.NewReader(response.Body)
	last := ""
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := discard(reader, interval); err != nil {
			return err
		}
		lengthByte, err := reader.ReadByte()
		if err != nil {
			return err
		}
		size := int(lengthByte) * 16
		if size == 0 {
			continue
		}
		if size > maxMetadataBytes {
			return errors.New("ICY metadata block too large")
		}
		block := make([]byte, size)
		if _, err := io.ReadFull(reader, block); err != nil {
			return err
		}
		update, ok := parse(string(block))
		if !ok {
			continue
		}
		if update.Title == last {
			continue
		}
		last = update.Title
		onUpdate(update)
	}
}

func discard(reader *bufio.Reader, n int) (int, error) {
	discarded := 0
	buffer := make([]byte, 4096)
	for discarded < n {
		size := n - discarded
		if size > len(buffer) {
			size = len(buffer)
		}
		read, err := reader.Read(buffer[:size])
		discarded += read
		if err != nil {
			return discarded, err
		}
	}
	return discarded, nil
}

// parse extracts StreamTitle from a metadata block such as
// "StreamTitle='Artist - Title';". It splits "Artist - Title" into parts.
func parse(block string) (Update, bool) {
	block = strings.TrimRight(block, "\x00")
	value, ok := metadataValue(block, "StreamTitle")
	if !ok || strings.TrimSpace(value) == "" {
		return Update{}, false
	}
	return splitStreamTitle(value), true
}

func metadataValue(block, key string) (string, bool) {
	marker := key + "='"
	index := strings.Index(block, marker)
	if index < 0 {
		return "", false
	}
	rest := block[index+len(marker):]
	end := strings.Index(rest, "'")
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

func splitStreamTitle(value string) Update {
	value = strings.TrimSpace(value)
	if index := strings.Index(value, " - "); index > 0 {
		return Update{Artist: strings.TrimSpace(value[:index]), Title: strings.TrimSpace(value[index+3:])}
	}
	return Update{Title: value}
}
