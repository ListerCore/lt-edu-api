// Package httpprobe asks a URL over HTTP and measures the answer: the availability log of the platform (phase E8)
package httpprobe

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"

	"api/internal/core/port"
)

// Probe implements port.UptimeProbe with a short timeout, a few redirects at most and a generic client name. Unless
// allowPrivate (development), it refuses to connect to loopback, private, link-local and other non-public addresses,
// on the first request and on every redirect, so the URL of the platform sheet cannot be used to scan the network
type Probe struct{ client *http.Client }

var errPrivate = errors.New("dirección no pública")

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() ||
		ip.IsMulticast() || ip.IsInterfaceLocalMulticast() || ip.Equal(net.IPv4bcast))
}

func New(timeout time.Duration, allowPrivate bool) *Probe {
	dialer := &net.Dialer{Timeout: timeout}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !publicIP(ip) {
				return errPrivate
			}
			return nil
		}
	}
	transport := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout, Proxy: nil}
	return &Probe{client: &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
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
