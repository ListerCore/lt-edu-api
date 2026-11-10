package grpc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"api/internal/adapter/inbound/grpc/gen/commonv1"
)

func TestRateLimiterWindow(t *testing.T) {
	l := newRateLimiter(2, time.Minute)
	now := time.Now()
	if !l.allowAt("a", now) || !l.allowAt("a", now) {
		t.Fatal("first two attempts must pass")
	}
	if l.allowAt("a", now) {
		t.Fatal("third attempt in the window must be refused")
	}
	if !l.allowAt("b", now) {
		t.Fatal("another key has its own window")
	}
	if !l.allowAt("a", now.Add(time.Minute)) {
		t.Fatal("a new window starts after the expiration")
	}
}

func callFrom(peerIP string, md metadata.MD) context.Context {
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP(peerIP), Port: 5000}})
	return metadata.NewIncomingContext(ctx, md)
}

func TestClientResolverTrustsOnlyProxies(t *testing.T) {
	r := newClientResolver([]string{"203.0.113.7", "198.51.100.0/24"}, zerolog.Nop())
	md := metadata.Pairs(forwardedForKey, "190.1.2.3, 10.0.0.1", userAgentKey, "Firefox")

	cases := map[string]string{
		"127.0.0.1":    "190.1.2.3", // loopback
		"10.1.2.3":     "190.1.2.3", // private
		"203.0.113.7":  "190.1.2.3", // listed IP
		"198.51.100.9": "190.1.2.3", // listed range
		"8.8.8.8":      "8.8.8.8",   // anyone else cannot spoof its IP
	}
	for peerIP, want := range cases {
		meta := r.resolve(callFrom(peerIP, md))
		if meta.IP != want {
			t.Errorf("peer %s: ip = %q, want %q", peerIP, meta.IP, want)
		}
		if meta.UserAgent != "Firefox" {
			t.Errorf("peer %s: user agent = %q", peerIP, meta.UserAgent)
		}
	}
}

func TestBearer(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer abc "))
	if got := bearer(ctx); got != "abc" {
		t.Fatalf("bearer = %q", got)
	}
	if got := bearer(context.Background()); got != "" {
		t.Fatalf("no metadata: bearer = %q", got)
	}
}

func TestToStatusCarriesErrorDetail(t *testing.T) {
	fault := Fault{Status: codes.InvalidArgument, Code: "validation", Message: "Revisa los datos.", Fields: map[string]string{"email": "Correo inválido."}}
	st := status.Convert(toStatus(fault))
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("code = %s", st.Code())
	}
	details := st.Details()
	if len(details) != 1 {
		t.Fatalf("details = %v", details)
	}
	detail, ok := details[0].(*commonv1.ErrorDetail)
	if !ok || detail.GetCode() != "validation" || detail.GetFields()["email"] != "Correo inválido." {
		t.Fatalf("detail = %v", details[0])
	}
}
