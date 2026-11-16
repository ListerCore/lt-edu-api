package domain

import "time"

// Forums of the virtual classroom (classroom.forums, posts, forum_reads)
const (
	ItemForum = "forum"

	ForumDebate    = "debate"
	ForumQuestions = "questions"
	ForumIntro     = "intro"
)

// Forum is a forum item: its kind, whether it is closed, and its posts
type Forum struct {
	ItemID       string
	RoomID       string
	UnitID       string
	Title        string
	Prompt       string
	Published    bool
	Kind         string
	Closed       bool
	ClosedByName string
}

// ForumPost is a thread (ParentID "") or a reply
type ForumPost struct {
	ID              string
	ItemID          string
	ParentID        string
	AuthorUserID    string
	AuthorStudentID string
	AuthorName      string
	AuthorRole      string
	Body            string
	Pinned          bool
	Hidden          bool
	HiddenReason    string
	HiddenByName    string
	CreatedAt       time.Time
	EditedAt        *time.Time
	// Read-only: replies of a thread
	Replies int
}

// ForumParticipation is how much a student of the room wrote in a forum
type ForumParticipation struct {
	StudentID   string
	StudentName string
	StudentCode string
	Threads     int
	Replies     int
	LastAt      *time.Time
}

var (
	ErrForumClosed    = newError(KindConflict, "forum_closed", "El foro está cerrado: ya no recibe mensajes.")
	ErrPostNotYours   = newError(KindForbidden, "post_not_yours", "Solo puedes cambiar tus propios mensajes.")
	ErrPostHasReplies = newError(KindConflict, "post_has_replies", "El mensaje ya tiene respuestas: edítalo en lugar de eliminarlo.")
	ErrPostHidden     = newError(KindConflict, "post_hidden", "El moderador ocultó este mensaje.")
	ErrReplyToReply   = newError(KindInvalid, "reply_to_reply", "Responde al mensaje principal del hilo.")
)
