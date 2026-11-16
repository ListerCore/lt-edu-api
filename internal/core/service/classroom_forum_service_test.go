package service

import (
	"testing"
	"time"

	"api/internal/core/domain"
)

func TestArrangeThreads(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	posts := []domain.ForumPost{
		{ID: "a", Body: "Hilo A", CreatedAt: at(0)},
		{ID: "b", Body: "Hilo B", CreatedAt: at(1)},
		{ID: "pin", Body: "Bienvenida", Pinned: true, CreatedAt: at(-60)},
		{ID: "a1", ParentID: "a", Body: "Respuesta en A", CreatedAt: at(5)},
		{ID: "x", ParentID: "b", Body: "Ofensivo", Hidden: true, HiddenReason: "Lenguaje inapropiado", CreatedAt: at(2)},
	}
	staff := arrangeThreads(posts, false)
	if len(staff) != 3 || staff[0].Post.ID != "pin" || staff[1].Post.ID != "a" || staff[2].Post.ID != "b" {
		t.Fatalf("order: pinned first, then latest activity; got %v %v %v", staff[0].Post.ID, staff[1].Post.ID, staff[2].Post.ID)
	}
	if len(staff[1].Replies) != 1 || staff[2].Replies[0].Body != "Ofensivo" {
		t.Fatal("staff read hidden posts")
	}
	family := arrangeThreads(posts, true)
	if hidden := family[2].Replies[0]; hidden.Body != "" || hidden.HiddenReason != "" || !hidden.Hidden {
		t.Fatalf("families must not read a hidden post: %+v", hidden)
	}
}

func TestCleanPostBody(t *testing.T) {
	if _, err := cleanPostBody("   "); err == nil {
		t.Fatal("empty post accepted")
	}
	if b, err := cleanPostBody("  Hola  "); err != nil || b != "Hola" {
		t.Fatalf("trim: %q %v", b, err)
	}
}
