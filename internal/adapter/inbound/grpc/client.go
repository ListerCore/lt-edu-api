package grpc

import (
	"context"
	"net/netip"
	"strings"

	"github.com/rs/zerolog"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	"api/internal/core/domain"
	"api/internal/core/service"
)

// Metadata keys lt-edu-app sends with every call (see proto/ltedu/system/v1/system.proto)
const (
	forwardedForKey = "x-forwarded-for"
	userAgentKey    = "x-user-agent"
	authorizationMD = "authorization"
)

type metaKey struct{}

// clientResolver works out who is calling. The visitor's IP comes from x-forwarded-for only when the connection
// itself comes from a trusted proxy (lt-edu-app), mirroring the HTTP API's TrustProxy settings
type clientResolver struct {
	trusted []netip.Prefix
}

func newClientResolver(proxies []string, logger zerolog.Logger) *clientResolver {
	r := &clientResolver{}
	for _, raw := range proxies {
		if prefix, err := netip.ParsePrefix(raw); err == nil {
			r.trusted = append(r.trusted, prefix.Masked())
			continue
		}
		if addr, err := netip.ParseAddr(raw); err == nil {
			r.trusted = append(r.trusted, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
			continue
		}
		logger.Warn().Str("proxy", raw).Msg("ignoring invalid trusted proxy for grpc")
	}
	return r
}

func (r *clientResolver) isTrusted(addr netip.Addr) bool {
	if addr.IsLoopback() || addr.IsPrivate() {
		return true
	}
	for _, prefix := range r.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// interceptor also opens the request memo, so every service of this call shares one lookup of the session
func (r *clientResolver) interceptor(ctx context.Context, req any, _ *gogrpc.UnaryServerInfo, handler gogrpc.UnaryHandler) (any, error) {
	return handler(service.WithRequestMemo(context.WithValue(ctx, metaKey{}, r.resolve(ctx))), req)
}

// wrappedStream hands the resolved RequestMeta to streaming handlers
type wrappedStream struct {
	gogrpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }

func (r *clientResolver) streamInterceptor(srv any, ss gogrpc.ServerStream, _ *gogrpc.StreamServerInfo, handler gogrpc.StreamHandler) error {
	// No memo here: a stream can outlive a session, and must see a revocation on its next lookup
	ctx := context.WithValue(ss.Context(), metaKey{}, r.resolve(ss.Context()))
	return handler(srv, &wrappedStream{ServerStream: ss, ctx: ctx})
}

func (r *clientResolver) resolve(ctx context.Context) domain.RequestMeta {
	var meta domain.RequestMeta
	var peerAddr netip.Addr
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		if addrPort, err := netip.ParseAddrPort(p.Addr.String()); err == nil {
			peerAddr = addrPort.Addr().Unmap()
			meta.IP = peerAddr.String()
		}
	}

	md, _ := metadata.FromIncomingContext(ctx)
	meta.UserAgent = first(md, userAgentKey)
	if meta.UserAgent == "" {
		meta.UserAgent = first(md, "user-agent")
	}
	if peerAddr.IsValid() && r.isTrusted(peerAddr) {
		forwarded := strings.TrimSpace(strings.Split(first(md, forwardedForKey), ",")[0])
		if addr, err := netip.ParseAddr(forwarded); err == nil {
			meta.IP = addr.Unmap().String()
		}
	}
	return meta
}

func requestMeta(ctx context.Context) domain.RequestMeta {
	meta, _ := ctx.Value(metaKey{}).(domain.RequestMeta)
	return meta
}

// bearer is the session token of the call (for services that need a signed-in user), sent as "authorization: Bearer <token>"
func bearer(ctx context.Context) string {
	md, _ := metadata.FromIncomingContext(ctx)
	token, found := strings.CutPrefix(first(md, authorizationMD), "Bearer ")
	if !found {
		return ""
	}
	return strings.TrimSpace(token)
}

func first(md metadata.MD, key string) string {
	if values := md.Get(key); len(values) > 0 {
		return values[0]
	}
	return ""
}
