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
	var s1Calls, s2Calls, s3Calls atomic.Int32

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

	// s3 standby
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s3Calls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("s3 ok"))
	}))
	defer s3.Close()

	pool := NewPoolWithMode([]string{s1.URL, s2.URL, s3.URL}, "", "fallback")

	// Request 1: hits s1 (fails 429), falls over to s2 (succeeds).
	// Cursor should shift to s2!
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "http://example.com/test1", nil)
	pool.Forward(rec1, req1, "http", "example.com", "/test1")
	if rec1.Code != http.StatusOK {
		t.Fatalf("request 1 failed with status %d", rec1.Code)
	}

	if s1Calls.Load() != 1 || s2Calls.Load() != 1 {
		t.Fatalf("after req 1: expected s1=1, s2=1; got s1=%d, s2=%d", s1Calls.Load(), s2Calls.Load())
	}

	// Requests 2..5: Should go directly to s2 WITHOUT hitting broken s1,
	// because cursor has sticky memory state on healthy node s2.
	for i := 2; i <= 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", fmt.Sprintf("http://example.com/test%d", i), nil)
		pool.Forward(rec, req, "http", "example.com", fmt.Sprintf("/test%d", i))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d failed with status %d", i, rec.Code)
		}
	}

	// s1 should STILL only have 1 call (tidak dicoba lagi, tidak menambah latency)
	if s1Calls.Load() != 1 {
		t.Errorf("expected s1 to not be retried (calls=1), but got %d", s1Calls.Load())
	}
	// s2 should have handled all 4 subsequent requests (total 5)
	if s2Calls.Load() != 5 {
		t.Errorf("expected s2 to handle 5 requests, got %d", s2Calls.Load())
	}
	// s3 should have 0 calls
	if s3Calls.Load() != 0 {
		t.Errorf("expected s3 calls=0, got %d", s3Calls.Load())
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
