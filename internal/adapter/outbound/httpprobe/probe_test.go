package httpprobe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbeRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	// Development reaches the local server
	if status, _, err := New(2*time.Second, true).Probe(context.Background(), srv.URL); err != nil || status != http.StatusNoContent {
		t.Fatalf("allowed: %d %v", status, err)
	}
	// Production refuses loopback, and a redirect to it
	if _, _, err := New(2*time.Second, false).Probe(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "no pública") {
		t.Fatalf("loopback: %v", err)
	}
	for _, url := range []string{"http://169.254.169.254/latest/meta-data", "http://10.0.0.1/", "http://[::1]:80/"} {
		if _, _, err := New(time.Second, false).Probe(context.Background(), url); err == nil || !strings.Contains(err.Error(), "no pública") {
			t.Errorf("%s: %v", url, err)
		}
	}
}
