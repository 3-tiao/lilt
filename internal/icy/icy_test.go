package icy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseStreamTitle(t *testing.T) {
	update, ok := parse("StreamTitle='Daft Punk - Around the World';")
	if !ok {
		t.Fatal("no title parsed")
	}
	if update.Artist != "Daft Punk" || update.Title != "Around the World" {
		t.Fatalf("update = %+v", update)
	}
	single, ok := parse("StreamTitle='Just A Title';")
	if !ok || single.Artist != "" || single.Title != "Just A Title" {
		t.Fatalf("single = %+v ok=%v", single, ok)
	}
	if _, ok := parse("StreamUrl='http://x';"); ok {
		t.Fatal("non-title block parsed as a title")
	}
	if _, ok := parse("StreamTitle='';"); ok {
		t.Fatal("empty title parsed")
	}
}

func TestWatchReadsMetadata(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Icy-MetaData") != "1" {
			t.Errorf("Icy-MetaData header = %q", r.Header.Get("Icy-MetaData"))
		}
		w.Header().Set("icy-metaint", "5")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		// 5 audio bytes, then a metadata block: 19 bytes padded to 32 (len byte 2).
		_, _ = w.Write([]byte("AAAAA"))
		payload := []byte("StreamTitle='A - B';")
		padded := make([]byte, 32)
		copy(padded, payload)
		_, _ = w.Write([]byte{2})
		_, _ = w.Write(padded)
		if flusher != nil {
			flusher.Flush()
		}
		// Preserve the connection until the test has observed the update.
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan Update, 1)
	go func() {
		_ = (&Client{}).Watch(ctx, server.URL, func(update Update) {
			select {
			case updates <- update:
			default:
			}
		})
	}()
	select {
	case update := <-updates:
		close(release)
		if update.Artist != "A" || update.Title != "B" {
			t.Fatalf("update = %+v", update)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for ICY update")
	}
}

func TestWatchWithoutMetaintReturns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("plain audio"))
	}))
	defer server.Close()
	done := make(chan error, 1)
	go func() {
		done <- (&Client{}).Watch(context.Background(), server.URL, func(Update) {})
	}()
	select {
	case err := <-done:
		if err != nil && !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not return for a stream without icy-metaint")
	}
}
