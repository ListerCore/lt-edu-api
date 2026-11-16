// Package httpprobe asks a URL over HTTP and measures the answer: the availability log of the platform (phase E8)
package httpprobe

import (
	"context"
	"io"
	"net/http"
	"time"

	"api/internal/core/port"
)

// Probe implements port.UptimeProbe with a short timeout, a few redirects at most and a generic client name
type Probe struct{ client *http.Client }

func New(timeout time.Duration) *Probe {
	return &Probe{client: &http.Client{Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		return nil
	}}}
}

var _ port.UptimeProbe = (*Probe)(nil)

func (p *Probe) Probe(ctx context.Context, url string) (int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("User-Agent", "ListerCore-Uptime/1.0")
	start := time.Now()
	res, err := p.client.Do(req)
	if err != nil {
		return 0, time.Since(start), err
	}
	defer res.Body.Close()
	// The page is not kept: read a little so the time covers the first bytes
	_, _ = io.CopyN(io.Discard, res.Body, 64<<10)
	return res.StatusCode, time.Since(start), nil
}
