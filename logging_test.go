package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestLoggingHandler(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		write  bool
	}{
		{"implicit", http.StatusOK, true},
		{"empty", http.StatusOK, false},
		{"error", http.StatusNotFound, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			handler := loggingHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status != http.StatusOK {
					w.WriteHeader(tc.status)
				}
				if tc.write {
					_, _ = w.Write([]byte("ok"))
				}
			}), log.New(&logs, "", 0))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/games?secret=private", nil))
			if response.Code != tc.status || !strings.Contains(logs.String(), "status="+strconv.Itoa(tc.status)) {
				t.Fatalf("status=%d log=%q", response.Code, logs.String())
			}
			if !strings.Contains(logs.String(), `method="GET" path="/api/games"`) || strings.Contains(logs.String(), "private") {
				t.Fatalf("unexpected log: %q", logs.String())
			}
		})
	}
}
