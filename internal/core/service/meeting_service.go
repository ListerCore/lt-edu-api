package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// MeetingService runs the live sessions (clases en vivo): live classes of the virtual classroom, virtual tutoring and
// psychology appointments. Who may enter each one comes from the module that owns it; this service lets them in,
// either with a LiveKit token for the call inside the platform or with the external link (Google Meet), and keeps
// who joined
type MeetingService struct {
	repo      port.MeetingsRepository
	auth      *AuthService
	classroom *ClassroomService
	wellbeing *WellbeingService
	livekit   LiveKitConfig
	now       func() time.Time
}

// LiveKitConfig is the LiveKit server the platform calls run on; an empty URL leaves only external links
type LiveKitConfig struct {
	URL       string
	APIKey    string
	APISecret string
}

func (c LiveKitConfig) Available() bool { return c.URL != "" && c.APIKey != "" && c.APISecret != "" }

func NewMeetingService(repo port.MeetingsRepository, auth *AuthService, classroom *ClassroomService, wellbeing *WellbeingService, livekit LiveKitConfig) *MeetingService {
	classroom.SetMeetings(repo)
	wellbeing.SetMeetings(repo)
	return &MeetingService{repo: repo, auth: auth, classroom: classroom, wellbeing: wellbeing, livekit: livekit, now: time.Now}
}

// PlatformAvailable tells the app whether "En la plataforma" can be offered
func (s *MeetingService) PlatformAvailable() bool { return s.livekit.Available() }

func (s *MeetingService) target(ctx context.Context, token, slug, source, id string) (domain.LiveTarget, error) {
	switch source {
	case domain.SourceClass:
		return s.classroom.LiveTarget(ctx, token, slug, id)
	case domain.SourceTutoring:
		return s.wellbeing.TutoringLiveTarget(ctx, token, slug, id)
	case domain.SourcePsychology:
		return s.wellbeing.PsychLiveTarget(ctx, token, slug, id)
	}
	return domain.LiveTarget{}, domain.ErrNotFound
}

// JoinInfo is how the person enters the call
type JoinInfo struct {
	Target domain.LiveTarget
	// meet: open MeetURL · platform: connect to ServerURL with Token to RoomName
	MeetURL   string
	ServerURL string
	Token     string
	RoomName  string
	Identity  string
	Name      string
}

// RoomName is the LiveKit room of a session
func RoomName(source, id string) string { return source + "-" + id }

// Join lets the person into the session: hosts any time, the rest from 15 minutes before the start until 30 minutes
// after the end. Each entry is logged (attendance of the call); a student's entry into a live class also counts as
// opening the item, which feeds participation
func (s *MeetingService) Join(ctx context.Context, token, slug, source, id string) (JoinInfo, error) {
	t, err := s.target(ctx, token, slug, source, id)
	if err != nil {
		return JoinInfo{}, err
	}
	now := s.now()
	if t.Cancelled {
		return JoinInfo{}, domain.ErrLiveCancelled
	}
	if !t.CanJoinAt(now) {
		return JoinInfo{}, domain.ErrLiveNotOpen
	}
	me, err := s.auth.Principal(ctx, token)
	if err != nil {
		return JoinInfo{}, err
	}
	name := strings.TrimSpace(me.User.FullName)
	if name == "" {
		name = me.User.Email
	}
	out := JoinInfo{Target: t, Identity: me.User.ID, Name: name}
	switch t.Provider {
	case domain.ProviderPlatform:
		if !s.livekit.Available() {
			return JoinInfo{}, domain.ErrLiveNotConfigured
		}
		out.ServerURL, out.RoomName = s.livekit.URL, RoomName(t.Source, t.ID)
		if out.Token, err = LiveKitToken(s.livekit, out.RoomName, me.User.ID, name, t.IsHost, now, t.Ends()); err != nil {
			return JoinInfo{}, err
		}
	default:
		if t.Link == "" {
			return JoinInfo{}, domain.ErrLiveNoLink
		}
		out.MeetURL = t.Link
	}
	if err := s.repo.RecordJoin(ctx, t.InstitutionID, t.Source, t.ID, me.User.ID, t.StudentID, name, now); err != nil {
		return JoinInfo{}, err
	}
	if t.Source == domain.SourceClass && t.StudentID != "" {
		if err := s.classroom.RecordLiveView(ctx, t.ID, t.StudentID); err != nil {
			return JoinInfo{}, err
		}
	}
	return out, nil
}

// Attendance is who joined the session; hosts only
func (s *MeetingService) Attendance(ctx context.Context, token, slug, source, id string) ([]domain.LiveJoin, error) {
	t, err := s.target(ctx, token, slug, source, id)
	if err != nil {
		return nil, err
	}
	if !t.IsHost {
		return nil, domain.ErrLiveForbidden
	}
	return s.repo.Joins(ctx, t.InstitutionID, t.Source, t.ID)
}

// Agenda is every live session of the person between from and to (at most 62 days), soonest first: their live
// classes, virtual tutoring and psychology appointments. A module the role does not reach adds nothing
func (s *MeetingService) Agenda(ctx context.Context, token, slug string, from, to time.Time) ([]domain.LiveTarget, error) {
	if !to.After(from) || to.Sub(from) > 62*24*time.Hour {
		return nil, domain.Validation(map[string]string{"to": "Elige un rango de hasta 62 días."})
	}
	out := []domain.LiveTarget{}
	sources := []func(context.Context, string, string, time.Time, time.Time) ([]domain.LiveTarget, error){
		s.classroom.LiveAgenda, s.wellbeing.TutoringLiveAgenda, s.wellbeing.PsychLiveAgenda,
	}
	for _, list := range sources {
		got, err := list(ctx, token, slug, from, to)
		if err != nil {
			if forbiddenKind(err) {
				continue
			}
			return nil, err
		}
		out = append(out, got...)
	}
	slices.SortStableFunc(out, func(a, b domain.LiveTarget) int { return a.StartsAt.Compare(b.StartsAt) })
	return out, nil
}

// forbiddenKind: a module closed to the role (or not set up) simply has no sessions for the agenda
func forbiddenKind(err error) bool {
	var de *domain.Error
	if !errors.As(err, &de) {
		return false
	}
	return de.Kind == domain.KindForbidden || errors.Is(err, domain.ErrNotFound)
}

// ---------- LiveKit access token ----------

const liveTokenMaxTTL = 6 * time.Hour

type liveVideoGrant struct {
	Room           string `json:"room"`
	RoomJoin       bool   `json:"roomJoin"`
	RoomAdmin      bool   `json:"roomAdmin,omitempty"`
	CanPublish     bool   `json:"canPublish"`
	CanSubscribe   bool   `json:"canSubscribe"`
	CanPublishData bool   `json:"canPublishData"`
}

type liveClaims struct {
	Iss   string         `json:"iss"`
	Sub   string         `json:"sub"`
	Name  string         `json:"name,omitempty"`
	Nbf   int64          `json:"nbf"`
	Exp   int64          `json:"exp"`
	Video liveVideoGrant `json:"video"`
}

// LiveKitToken signs the LiveKit access token (an HS256 JWT) for one person in one room. It is valid from now until
// an hour after the session ends, never longer than six hours
func LiveKitToken(c LiveKitConfig, room, identity, name string, host bool, now, ends time.Time) (string, error) {
	exp := ends.Add(time.Hour)
	if exp.Sub(now) > liveTokenMaxTTL {
		exp = now.Add(liveTokenMaxTTL)
	}
	if !exp.After(now) {
		exp = now.Add(time.Hour)
	}
	claims := liveClaims{Iss: c.APIKey, Sub: identity, Name: name, Nbf: now.Add(-10 * time.Second).Unix(), Exp: exp.Unix(),
		Video: liveVideoGrant{Room: room, RoomJoin: true, RoomAdmin: host, CanPublish: true, CanSubscribe: true, CanPublishData: true}}
	header, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	unsigned := enc.EncodeToString(header) + "." + enc.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(c.APISecret))
	mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil)), nil
}
