package service

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"api/internal/core/domain"
)

const maxPostLength = 5000

var forumKinds = map[string]bool{domain.ForumDebate: true, domain.ForumQuestions: true, domain.ForumIntro: true}

// ForumView is a forum as the person sees it: its threads with their replies, what is new since their last visit,
// what they may do and, for staff, the participation of each student
type ForumView struct {
	Forum       domain.Forum
	Threads     []ForumThread
	CanPost     bool
	CanModerate bool
	MyUserID    string
	LastRead    *time.Time
	NewPosts    int
	// Staff only
	Participation []domain.ForumParticipation
}

// ForumThread is a thread and its replies, oldest first
type ForumThread struct {
	Post    domain.ForumPost
	Replies []domain.ForumPost
}

// forumAccess loads the forum and the person's access to its room; families need it published in an open unit
func (s *ClassroomService) forumAccess(ctx context.Context, token, slug, itemID string) (classroomScope, domain.Forum, bool, string, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return sc, domain.Forum{}, false, "", err
	}
	f, err := s.repo.Forum(ctx, sc.institution.ID, itemID)
	if err != nil {
		return sc, f, false, "", err
	}
	_, canEdit, viewer, err := s.roomAccess(ctx, sc, f.RoomID)
	if err != nil {
		return sc, f, false, "", err
	}
	if sc.family() {
		u, err := s.repo.Unit(ctx, sc.institution.ID, f.UnitID)
		if err != nil {
			return sc, f, false, "", err
		}
		if !f.Published || !u.OpenOn(s.now()) {
			return sc, f, false, "", domain.ErrNotFound
		}
	}
	return sc, f, canEdit, viewer, nil
}

// arrangeThreads groups replies under their thread (pinned threads first, then newest activity first) and blanks the
// text of hidden posts for families
func arrangeThreads(posts []domain.ForumPost, family bool) []ForumThread {
	byID := map[string]int{}
	threads := []ForumThread{}
	for _, p := range posts {
		if family && p.Hidden {
			p.Body, p.HiddenReason = "", ""
		}
		if p.ParentID == "" {
			byID[p.ID] = len(threads)
			threads = append(threads, ForumThread{Post: p, Replies: []domain.ForumPost{}})
		}
	}
	for _, p := range posts {
		if family && p.Hidden {
			p.Body, p.HiddenReason = "", ""
		}
		if i, ok := byID[p.ParentID]; ok {
			threads[i].Replies = append(threads[i].Replies, p)
		}
	}
	lastActivity := func(t ForumThread) time.Time {
		last := t.Post.CreatedAt
		if n := len(t.Replies); n > 0 && t.Replies[n-1].CreatedAt.After(last) {
			last = t.Replies[n-1].CreatedAt
		}
		return last
	}
	// Stable insertion sort keeps it simple for forum sizes
	for i := 1; i < len(threads); i++ {
		for j := i; j > 0; j-- {
			a, b := threads[j-1], threads[j]
			if (b.Post.Pinned && !a.Post.Pinned) || (b.Post.Pinned == a.Post.Pinned && lastActivity(b).After(lastActivity(a))) {
				threads[j-1], threads[j] = b, a
				continue
			}
			break
		}
	}
	return threads
}

// Forum reads the forum and marks it read for the person, returning how many posts by others arrived since their
// previous visit
func (s *ClassroomService) Forum(ctx context.Context, token, slug, itemID string) (ForumView, error) {
	sc, f, canEdit, viewer, err := s.forumAccess(ctx, token, slug, itemID)
	if err != nil {
		return ForumView{}, err
	}
	posts, err := s.repo.Posts(ctx, itemID)
	if err != nil {
		return ForumView{}, err
	}
	last, err := s.repo.LastRead(ctx, itemID, sc.userID)
	if err != nil {
		return ForumView{}, err
	}
	view := ForumView{Forum: f, Threads: arrangeThreads(posts, sc.family()), CanModerate: canEdit, MyUserID: sc.userID, LastRead: last}
	// Students post in an open forum; the room's teachers always can (also to answer in a closed one)
	view.CanPost = canEdit || (viewer != "" && !f.Closed)
	for _, p := range posts {
		if p.AuthorUserID != sc.userID && (last == nil || p.CreatedAt.After(*last)) {
			view.NewPosts++
		}
	}
	if !sc.family() {
		part, err := s.repo.ForumParticipation(ctx, sc.institution.ID, itemID)
		if err != nil {
			return ForumView{}, err
		}
		view.Participation = part
	}
	if sc.userID != "" {
		if err := s.repo.MarkRead(ctx, itemID, sc.userID, s.now()); err != nil {
			return ForumView{}, err
		}
	}
	return view, nil
}

func cleanPostBody(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" || utf8.RuneCountInString(body) > maxPostLength {
		return "", domain.Validation(map[string]string{"body": "Escribe tu mensaje (hasta 5000 caracteres)."})
	}
	return body, nil
}

// Post writes a thread (parentID "") or a reply. Students write in an open forum of their room; the room's teachers
// also in a closed one. Guardians only read
func (s *ClassroomService) Post(ctx context.Context, token, slug, itemID, parentID, body string) (domain.ForumPost, error) {
	sc, f, canEdit, viewer, err := s.forumAccess(ctx, token, slug, itemID)
	if err != nil {
		return domain.ForumPost{}, err
	}
	if !canEdit && viewer == "" {
		return domain.ForumPost{}, domain.ErrClassroomReadOnly
	}
	if f.Closed && !canEdit {
		return domain.ForumPost{}, domain.ErrForumClosed
	}
	body, err = cleanPostBody(body)
	if err != nil {
		return domain.ForumPost{}, err
	}
	out, err := s.repo.CreatePost(ctx, domain.ForumPost{ItemID: itemID, ParentID: parentID, AuthorUserID: sc.userID, AuthorStudentID: viewer,
		AuthorName: sc.userName, AuthorRole: sc.role, Body: body})
	if err != nil {
		return domain.ForumPost{}, err
	}
	// Writing in a forum is participation too
	if viewer != "" {
		_ = s.repo.RecordView(ctx, itemID, viewer, s.now())
	}
	s.changed(ctx, sc, "forum", f.RoomID)
	return out, nil
}

// ownPost loads a post of the signed-in person
func (s *ClassroomService) ownPost(ctx context.Context, token, slug, postID string) (classroomScope, domain.ForumPost, domain.Forum, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return sc, domain.ForumPost{}, domain.Forum{}, err
	}
	p, err := s.repo.Post(ctx, sc.institution.ID, postID)
	if err != nil {
		return sc, p, domain.Forum{}, err
	}
	_, f, _, _, err := s.forumAccess(ctx, token, slug, p.ItemID)
	if err != nil {
		return sc, p, f, err
	}
	if p.AuthorUserID == "" || p.AuthorUserID != sc.userID {
		return sc, p, f, domain.ErrPostNotYours
	}
	return sc, p, f, nil
}

func (s *ClassroomService) EditPost(ctx context.Context, token, slug, postID, body string) error {
	sc, _, f, err := s.ownPost(ctx, token, slug, postID)
	if err != nil {
		return err
	}
	body, err = cleanPostBody(body)
	if err != nil {
		return err
	}
	if err := s.repo.EditPost(ctx, postID, body, s.now()); err != nil {
		return err
	}
	s.changed(ctx, sc, "forum", f.RoomID)
	return nil
}

// DeletePost lets authors remove their own post while nobody has replied to it
func (s *ClassroomService) DeletePost(ctx context.Context, token, slug, postID string) error {
	sc, _, f, err := s.ownPost(ctx, token, slug, postID)
	if err != nil {
		return err
	}
	if err := s.repo.DeletePost(ctx, postID); err != nil {
		return err
	}
	s.changed(ctx, sc, "forum", f.RoomID)
	return nil
}

// ModerateInput pins a thread, or hides or shows a post with the reason families read
type ModerateInput struct {
	PostID string
	Pinned bool
	Hidden bool
	Reason string
}

// ModeratePost is for the room's teachers (and dirección, coordinación): hiding needs a reason and goes to the audit
func (s *ClassroomService) ModeratePost(ctx context.Context, token, slug string, in ModerateInput) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	p, err := s.repo.Post(ctx, sc.institution.ID, in.PostID)
	if err != nil {
		return err
	}
	_, f, canEdit, _, err := s.forumAccess(ctx, token, slug, p.ItemID)
	if err != nil {
		return err
	}
	if !canEdit {
		return domain.ErrClassroomReadOnly
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Hidden && (in.Reason == "" || utf8.RuneCountInString(in.Reason) > 300) {
		return domain.Validation(map[string]string{"reason": "Escribe por qué lo ocultas (hasta 300 caracteres)."})
	}
	if in.Pinned && p.ParentID != "" {
		return domain.Validation(map[string]string{"pinned": "Solo se fijan los mensajes principales."})
	}
	wasHidden := p.Hidden
	p.Pinned, p.Hidden = in.Pinned, in.Hidden
	if in.Hidden {
		p.HiddenReason, p.HiddenByName = in.Reason, sc.userName
	} else {
		p.HiddenReason, p.HiddenByName = "", ""
	}
	if err := s.repo.ModeratePost(ctx, p); err != nil {
		return err
	}
	if in.Hidden != wasHidden {
		verb := "Ocultó"
		if !in.Hidden {
			verb = "Volvió a mostrar"
		}
		s.audit.Record(ctx, sc.institutionScope, AuditEntry{Module: "classroom", Action: domain.AuditStatus, Entity: "forum_post", EntityID: p.ID,
			Summary: verb + " un mensaje de " + p.AuthorName + " en el foro «" + f.Title + "»", Changes: map[string]any{"reason": in.Reason}})
	}
	s.changed(ctx, sc, "forum", f.RoomID)
	return nil
}

// SaveForum sets the kind and opens or closes the forum
func (s *ClassroomService) SaveForum(ctx context.Context, token, slug, itemID, kind string, closed bool) error {
	sc, f, canEdit, _, err := s.forumAccess(ctx, token, slug, itemID)
	if err != nil {
		return err
	}
	if !canEdit {
		return domain.ErrClassroomReadOnly
	}
	if !forumKinds[kind] {
		return domain.Validation(map[string]string{"kind": "Elige el tipo de foro."})
	}
	f.Kind = kind
	if closed != f.Closed {
		f.ClosedByName = ""
		if closed {
			f.ClosedByName = sc.userName
		}
	}
	f.Closed = closed
	if err := s.repo.SaveForum(ctx, sc.institution.ID, f); err != nil {
		return err
	}
	s.changed(ctx, sc, "forum", f.RoomID)
	return nil
}
