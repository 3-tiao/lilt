package mpvplayer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeHealthyStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte{0xff, 0xfb, 0x90, 0x00})
	}))
	defer server.Close()

	client := &Client{HTTP: server.Client()}
	result, err := client.Probe(context.Background(), server.URL+"/stream", 5000)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.Status != "healthy" {
		t.Fatalf("result = %+v, want healthy", result)
	}
}

func TestProbeHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := &Client{HTTP: server.Client()}
	result, err := client.Probe(context.Background(), server.URL+"/stream", 5000)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.Status != "failed" || result.ErrorCode != "http" {
		t.Fatalf("result = %+v, want failed/http", result)
	}
}

func TestProbeStreamClosedBeforeAudio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &Client{HTTP: server.Client()}
	result, err := client.Probe(context.Background(), server.URL+"/stream", 5000)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.Status != "failed" || result.ErrorCode != "network" {
		t.Fatalf("result = %+v, want failed/network", result)
	}
}

func TestProbeTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
	}))
	defer server.Close()

	client := &Client{HTTP: server.Client()}
	result, err := client.Probe(context.Background(), server.URL+"/stream", 200)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.Status != "failed" || result.ErrorCode != "timeout" {
		t.Fatalf("result = %+v, want failed/timeout", result)
	}
}

func TestProbeRejectsNonHTTPURL(t *testing.T) {
	client := &Client{}
	result, err := client.Probe(context.Background(), "rtsp://example.test/stream", 5000)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.Status != "failed" || result.ErrorCode != "unsupported" {
		t.Fatalf("result = %+v, want failed/unsupported", result)
	}
}

func TestProbeDoesNotHangPastItsTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
	}))
	defer server.Close()

	client := &Client{HTTP: server.Client()}
	started := time.Now()
	if _, err := client.Probe(context.Background(), server.URL+"/stream", 150); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("probe took %s; it must honor its timeout", elapsed)
	}
}
