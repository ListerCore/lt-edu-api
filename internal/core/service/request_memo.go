package service

import (
	"context"
	"sync"

	"api/internal/core/domain"
)

// requestMemo remembers who is calling for the length of one request, so the services that scope that request
// (attendance, then assessment, then academic…) do not read the session, user and roles again each time. It never
// outlives the request: a revoked session or a changed role shows up on the very next call
type requestMemo struct {
	mu sync.Mutex
	me map[string]domain.Me
}

type requestMemoKey struct{}

// WithRequestMemo gives a request its memo; the inbound adapters call it once per incoming call
func WithRequestMemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestMemoKey{}, &requestMemo{me: map[string]domain.Me{}})
}

func (m *requestMemo) get(token string) (domain.Me, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	me, ok := m.me[token]
	return me, ok
}

func (m *requestMemo) put(token string, me domain.Me) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.me[token] = me
}

// RequestUser is the id of the user the request was resolved to ("" when no service looked the session up), for
// the request log: a refused call (another institution's record, a module turned off) names who made it
func RequestUser(ctx context.Context) string {
	memo, _ := ctx.Value(requestMemoKey{}).(*requestMemo)
	if memo == nil {
		return ""
	}
	memo.mu.Lock()
	defer memo.mu.Unlock()
	for _, me := range memo.me {
		return me.User.ID
	}
	return ""
}
