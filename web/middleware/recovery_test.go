package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecovery_NoPanic(t *testing.T) {
	handler := Recovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if rr.Body.String() != "ok" {
		t.Errorf("expected 'ok', got %q", rr.Body.String())
	}
}

func TestRecovery_PanicString(t *testing.T) {
	handler := Recovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went wrong")
	}))

	req := httptest.NewRequest("GET", "/panic", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "internal server error") {
		t.Errorf("expected error body, got %q", rr.Body.String())
	}
}

func TestRecovery_PanicError(t *testing.T) {
	handler := Recovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(42) // non-string panic value
	}))

	req := httptest.NewRequest("GET", "/panic-int", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rr.Code)
	}
}

func TestRecovery_PanicNilPointer(t *testing.T) {
	handler := Recovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var s *string
		_ = *s // nil pointer dereference
	}))

	req := httptest.NewRequest("GET", "/nil-deref", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rr.Code)
	}
}

func TestRecovery_DoesNotCatchSubsequentRequests(t *testing.T) {
	callCount := 0
	handler := Recovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			panic("first request panics")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("recovered"))
	}))

	// First request panics
	req1 := httptest.NewRequest("GET", "/first", nil)
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusInternalServerError {
		t.Errorf("first request: expected 500, got %d", rr1.Code)
	}

	// Second request succeeds — server is still alive
	req2 := httptest.NewRequest("GET", "/second", nil)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Errorf("second request: expected 200, got %d", rr2.Code)
	}
	if rr2.Body.String() != "recovered" {
		t.Errorf("second request: expected 'recovered', got %q", rr2.Body.String())
	}
}

func TestRecovery_HeadersNotSentBeforePanic(t *testing.T) {
	handler := Recovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Panic before writing any response
		panic("early panic")
	}))

	req := httptest.NewRequest("GET", "/early", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Should get our 500 error, not a 200
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Errorf("expected text/plain content type from http.Error, got %q", ct)
	}
}
