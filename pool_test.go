package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRelayPoolModeFallback(t *testing.T) {
	var s1Calls, s2Calls atomic.Int32

	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s1Calls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("s1 ok"))
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s2Calls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("s2 ok"))
	}))
	defer s2.Close()

	pool := NewPoolWithMode([]string{s1.URL, s2.URL}, "", "fallback")
	if pool.Mode() != "fallback" {
		t.Fatalf("expected mode 'fallback', got %s", pool.Mode())
	}

	// 5 requests in fallback mode should all go to primary (s1) when s1 is healthy
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "http://example.com/test", nil)
		pool.Forward(rec, req, "http", "example.com", "/test")
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d failed with status %d", i, rec.Code)
		}
	}

	if s1Calls.Load() != 5 {
		t.Errorf("expected 5 calls to s1, got %d", s1Calls.Load())
	}
	if s2Calls.Load() != 0 {
		t.Errorf("expected 0 calls to s2, got %d", s2Calls.Load())
	}
}

func TestRelayPoolModeFallbackFailover(t *testing.T) {
	var s1Calls, s2Calls atomic.Int32

	// s1 fails with 429
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s1Calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer s1.Close()

	// s2 succeeds
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s2Calls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("s2 ok"))
	}))
	defer s2.Close()

	pool := NewPoolWithMode([]string{s1.URL, s2.URL}, "", "fallback")

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "http://example.com/test", nil)
		pool.Forward(rec, req, "http", "example.com", "/test")
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d failed with status %d", i, rec.Code)
		}
	}

	// Every request should try s1 first, fail, then fallback to s2
	if s1Calls.Load() != 3 {
		t.Errorf("expected 3 calls to s1, got %d", s1Calls.Load())
	}
	if s2Calls.Load() != 3 {
		t.Errorf("expected 3 calls to s2, got %d", s2Calls.Load())
	}
}

func TestRelayPoolModeRoundRobin(t *testing.T) {
	var s1Calls, s2Calls atomic.Int32

	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s1Calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s2Calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer s2.Close()

	pool := NewPoolWithMode([]string{s1.URL, s2.URL}, "", "round_robin")
	if pool.Mode() != "round_robin" {
		t.Fatalf("expected mode 'round_robin', got %s", pool.Mode())
	}

	// 4 requests should alternate 2 to s1 and 2 to s2
	for i := 0; i < 4; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", fmt.Sprintf("http://example.com/test%d", i), nil)
		pool.Forward(rec, req, "http", "example.com", "/test")
	}

	if s1Calls.Load() != 2 || s2Calls.Load() != 2 {
		t.Errorf("expected 2 calls each, got s1=%d, s2=%d", s1Calls.Load(), s2Calls.Load())
	}
}
