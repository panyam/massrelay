package middleware

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestReadHeaderTimeout verifies that a server with ReadHeaderTimeout
// closes connections from slow clients that don't send headers in time.
func TestReadHeaderTimeout(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Start a server with a very short ReadHeaderTimeout
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 100 * time.Millisecond,
	}
	go srv.Serve(ln)
	defer srv.Close()

	addr := ln.Addr().String()

	// Test 1: Fast client succeeds
	t.Run("fast client succeeds", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		// Send a complete request immediately
		fmt.Fprintf(conn, "GET /test HTTP/1.1\r\nHost: localhost\r\n\r\n")
		conn.SetReadDeadline(time.Now().Add(time.Second))

		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatalf("fast client failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
	})

	// Test 2: Slow client gets disconnected
	t.Run("slow client times out", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		// Send partial headers only (no \r\n\r\n terminator)
		fmt.Fprintf(conn, "GET /slow HTTP/1.1\r\nHost: localhost\r\n")

		// Wait longer than ReadHeaderTimeout
		time.Sleep(300 * time.Millisecond)

		// Try to read — should get an error or timeout response
		conn.SetReadDeadline(time.Now().Add(time.Second))
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			// Connection closed by server — expected
			return
		}
		// Some Go versions return 408 Request Timeout instead of closing
		if resp.StatusCode != http.StatusRequestTimeout {
			t.Errorf("expected connection close or 408, got %d", resp.StatusCode)
		}
	})
}

// TestMaxHeaderBytes verifies that oversized headers are rejected.
func TestMaxHeaderBytes(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    512, // very small for testing (Go adds 4096 internally)
	}
	go srv.Serve(ln)
	defer srv.Close()

	addr := ln.Addr().String()

	// Send a request with a header much larger than MaxHeaderBytes + 4KB overhead
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	bigHeader := strings.Repeat("X", 8192)
	fmt.Fprintf(conn, "GET /big HTTP/1.1\r\nHost: localhost\r\nX-Big: %s\r\n\r\n", bigHeader)
	conn.SetReadDeadline(time.Now().Add(time.Second))

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		// Connection closed — acceptable
		return
	}
	// Go returns 431 Request Header Fields Too Large
	if resp.StatusCode != 431 {
		t.Errorf("expected 431, got %d", resp.StatusCode)
	}
}
