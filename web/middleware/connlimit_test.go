package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestConnLimiter_Nil(t *testing.T) {
	c := NewConnLimiter(0)
	if c != nil {
		t.Error("expected nil for max=0")
	}
	// Nil middleware should pass through
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := c.Middleware(ok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 {
		t.Errorf("got %d, want 200", w.Code)
	}
}

func TestConnLimiter_RejectsOverLimit(t *testing.T) {
	c := NewConnLimiter(2)
	blocker := make(chan struct{})

	// Handler that blocks until signaled
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocker
		w.WriteHeader(200)
	})
	h := c.Middleware(slow)

	// Fill up both slots
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		}()
	}

	// Wait for both to be active
	for c.Active() < 2 {
		// spin until both are active
	}

	// Third request should be rejected
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", w.Code)
	}

	// Release blocked handlers
	close(blocker)
	wg.Wait()

	// After release, counter should be back to 0
	if c.Active() != 0 {
		t.Errorf("active = %d, want 0 after release", c.Active())
	}

	// New request should succeed
	fast := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h2 := c.Middleware(fast)
	w2 := httptest.NewRecorder()
	h2.ServeHTTP(w2, httptest.NewRequest("GET", "/", nil))
	if w2.Code != 200 {
		t.Errorf("got %d, want 200 after slots freed", w2.Code)
	}
}

func TestConnLimiter_Active(t *testing.T) {
	c := NewConnLimiter(10)
	if c.Active() != 0 {
		t.Errorf("initial active = %d, want 0", c.Active())
	}

	// Nil limiter returns 0
	var nilC *ConnLimiter
	if nilC.Active() != 0 {
		t.Errorf("nil active = %d, want 0", nilC.Active())
	}
}
