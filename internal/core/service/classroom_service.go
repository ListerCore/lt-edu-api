package service

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// EventClassroomChanged: {institution, area (rooms | room | library | deliveries), room}
const EventClassroomChanged = "classroom.changed"

const (
	maxItemBody = 20000
	maxUnitsIDs = 200
)

var (
	// Dirección and coordinación manage every room; teachers (and tutors, who also teach) edit the rooms of the
	// subjects they teach; the Responsable técnico and secretaría read them all (evidence for the DRE, support)
	classroomManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true}
	classroomTeachers = map[string]bool{"teacher": true, "tutor": true, "program_coordinator": true}
	classroomReaders  = map[string]bool{"secretary": true, domain.RoleTechnicalLead: true, "psychologist": true}
	classroomFamilies = map[string]bool{"student": true, "guardian": true}

	itemKinds    = map[string]bool{domain.ItemReading: true, domain.ItemFile: true, domain.ItemVideo: true, domain.ItemLink: true, domain.ItemLive: true, domain.ItemActivity: true, domain.ItemQuiz: true, domain.ItemTask: true, domain.ItemForum: true}
	libraryKinds = map[string]bool{domain.ItemReading: true, domain.ItemFile: true, domain.ItemVideo: true, domain.ItemLink: true}
	httpsPattern = regexp.MustCompile(`^https://[^\s<>"]+$`)
)

// ClassroomService runs the virtual classroom (sección de aula virtual del Anexo 2): a room per subject and grade
// of each period, its units and material, the library and the itinerant deliveries. Students and their families
// only reach published rooms of the student's grade, open units and published material
type ClassroomService struct {
	repo   port.ClassroomRepository
	auth   *AuthService
	live   *LiveService
	audit  *AuditService
	grades *AssessmentService
	now    func() time.Time
}

func NewClassroomService(repo port.ClassroomRepository, auth *AuthService, live *LiveService, audit *AuditService) *ClassroomService {
	return &ClassroomService{repo: repo, auth: auth, live: live, audit: audit, now: time.Now}
}

type classroomScope struct {
	institutionScope
	email    string
	userName string
	// Staff record of a teacher ("" for others)
	staffID string
}

func (sc classroomScope) manager() bool { return classroomManagers[sc.role] }
func (sc classroomScope) family() bool  { return classroomFamilies[sc.role] }

func (s *ClassroomService) scope(ctx context.Context, token, slug string) (classroomScope, error) {
	isc, err := resolveInstitution(ctx, s.auth, s.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return classroomScope{}, err
	}
	if !classroomManagers[isc.role] && !classroomTeachers[isc.role] && !classroomReaders[isc.role] && !classroomFamilies[isc.role] {
		return classroomScope{}, domain.ErrClassroomForbidden
	}
	me, err := s.auth.Me(ctx, token)
	if err != nil {
		return classroomScope{}, err
	}
	sc := classroomScope{institutionScope: isc, email: me.User.Email, userName: uploaderName(me.User)}
	if classroomTeachers[isc.role] {
		st, err := s.repo.StaffByEmail(ctx, isc.institution.ID, me.User.Email)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return classroomScope{}, err
		}
		sc.staffID = st.ID
	}
	return sc, nil
}

func (s *ClassroomService) changed(ctx context.Context, sc classroomScope, area, roomID string) {
	s.live.Notify(ctx, EventClassroomChanged, map[string]string{"institution": sc.institution.Slug, "area": area, "room": roomID})
}

// period picks the requested period, or the running one
func (s *ClassroomService) period(ctx context.Context, sc classroomScope, periodID string) (string, []domain.Period, error) {
	periods, err := s.repo.Periods(ctx, sc.institution.ID)
	if err != nil {
		return "", nil, err
	}
	if periodID != "" {
		if !slices.ContainsFunc(periods, func(p domain.Period) bool { return p.ID == periodID }) {
			return "", nil, domain.ErrNotFound
		}
		return periodID, periods, nil
	}
	running, err := s.repo.RunningPeriod(ctx, sc.institution.ID)
	if err != nil {
		return "", nil, err
	}
	if running == "" && len(periods) > 0 {
		running = periods[0].ID
	}
	return running, periods, nil
}

// ClassroomCatalog is the Aula virtual page: the rooms the person reaches in a period and what they may do
type ClassroomCatalog struct {
	Periods   []domain.Period
	PeriodID  string
	Rooms     []domain.Room
	CanManage bool
	// teacher: edits their rooms · reader: reads all · family: a student's (or their child's) rooms
	Mode string
}

func (s *ClassroomService) Catalog(ctx context.Context, token, slug, periodID string) (ClassroomCatalog, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return ClassroomCatalog{}, err
	}
	periodID, periods, err := s.period(ctx, sc, periodID)
	if err != nil {
		return ClassroomCatalog{}, err
	}
	out := ClassroomCatalog{Periods: periods, PeriodID: periodID, CanManage: sc.manager(), Rooms: []domain.Room{}}
	if periodID == "" {
		return out, nil
	}
	f := domain.RoomFilter{PeriodID: periodID}
	switch {
	case sc.manager():
		out.Mode = "manager"
	case classroomTeachers[sc.role]:
		out.Mode = "teacher"
		if sc.staffID == "" {
			return out, nil
		}
		f.StaffID = sc.staffID
	case sc.family():
		out.Mode = "family"
		grades, err := s.repo.StudentGrades(ctx, sc.institution.ID, periodID, sc.email)
		if err != nil {
			return ClassroomCatalog{}, err
		}
		f.GradeIDs, f.PublishedOnly = []string{}, true
		for _, g := range grades {
			if g != "" && !slices.Contains(f.GradeIDs, g) {
				f.GradeIDs = append(f.GradeIDs, g)
			}
		}
	default:
		out.Mode = "reader"
	}
	rooms, err := s.repo.Rooms(ctx, sc.institution.ID, f)
	if err != nil {
		return ClassroomCatalog{}, err
	}
	out.Rooms = rooms
	return out, nil
}

// GenerateRooms creates the missing rooms of a period from the plans of study in force
func (s *ClassroomService) GenerateRooms(ctx context.Context, token, slug, periodID string) (int, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return 0, err
	}
	if !sc.manager() {
		return 0, domain.ErrClassroomReadOnly
	}
	if _, _, err := s.period(ctx, sc, periodID); err != nil || periodID == "" {
		if err == nil {
			err = domain.ErrNotFound
		}
		return 0, err
	}
	n, err := s.repo.EnsureRooms(ctx, sc.institution.ID, periodID)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		s.audit.Record(ctx, sc.institutionScope, AuditEntry{Module: "classroom", Action: domain.AuditCreate, Entity: "room", EntityID: periodID,
			Summary: "Generó " + itoa(n) + " aulas virtuales del plan de estudios"})
		s.changed(ctx, sc, "rooms", "")
	}
	return n, nil
}

// roomAccess loads a room and tells whether the person can edit it; families must be students of its grade and the
// room published. viewer is the student whose views are marked ("" for staff and for a guardian of several)
func (s *ClassroomService) roomAccess(ctx context.Context, sc classroomScope, roomID string) (room domain.Room, canEdit bool, viewer string, err error) {
	room, err = s.repo.Room(ctx, sc.institution.ID, roomID)
	if err != nil {
		return room, false, "", err
	}
	switch {
	case sc.manager():
		return room, true, "", nil
	case classroomTeachers[sc.role]:
		if sc.staffID == "" {
			return room, false, "", domain.ErrRoomNotYours
		}
		ok, err := s.repo.TeachesRoom(ctx, sc.institution.ID, roomID, sc.staffID)
		if err != nil {
			return room, false, "", err
		}
		if !ok {
			return room, false, "", domain.ErrRoomNotYours
		}
		return room, true, "", nil
	case sc.family():
		if room.Status != domain.RoomPublished {
			return room, false, "", domain.ErrRoomNotYours
		}
		grades, err := s.repo.StudentGrades(ctx, sc.institution.ID, room.PeriodID, sc.email)
		if err != nil {
			return room, false, "", err
		}
		for student, grade := range grades {
			if grade == room.GradeID {
				// Only the student's own openings count; a guardian only reads
				if sc.role == "student" {
					viewer = student
				}
				return room, false, viewer, nil
			}
		}
		return room, false, "", domain.ErrRoomNotYours
	default:
		return room, false, "", nil
	}
}

// RoomDetail is one room with its units. Families get only open units and published material
type RoomDetail struct {
	Room    domain.Room
	Units   []domain.Unit
	CanEdit bool
	Family  bool
}

func (s *ClassroomService) Room(ctx context.Context, token, slug, roomID string) (RoomDetail, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return RoomDetail{}, err
	}
	room, canEdit, viewer, err := s.roomAccess(ctx, sc, roomID)
	if err != nil {
		return RoomDetail{}, err
	}
	units, err := s.repo.Units(ctx, roomID, !sc.family(), viewer)
	if err != nil {
		return RoomDetail{}, err
	}
	if sc.family() {
		units = visibleUnits(units, s.now())
	}
	return RoomDetail{Room: room, Units: units, CanEdit: canEdit, Family: sc.family()}, nil
}

// visibleUnits keeps what a student sees on day: units published and open, with their published material
func visibleUnits(units []domain.Unit, day time.Time) []domain.Unit {
	out := []domain.Unit{}
	for _, u := range units {
		if !u.OpenOn(day) {
			continue
		}
		items := []domain.Item{}
		for _, it := range u.Items {
			if it.Published {
				items = append(items, it)
			}
		}
		u.Items = items
		out = append(out, u)
	}
	return out
}

func (s *ClassroomService) editable(ctx context.Context, token, slug, roomID string) (classroomScope, domain.Room, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return sc, domain.Room{}, err
	}
	room, canEdit, _, err := s.roomAccess(ctx, sc, roomID)
	if err != nil {
		return sc, room, err
	}
	if !canEdit {
		return sc, room, domain.ErrClassroomReadOnly
	}
	return sc, room, nil
}

// UpdateRoom edits the description and publication; only dirección and coordinación change the teacher in charge
func (s *ClassroomService) UpdateRoom(ctx context.Context, token, slug string, in domain.Room) (domain.Room, error) {
	sc, room, err := s.editable(ctx, token, slug, in.ID)
	if err != nil {
		return domain.Room{}, err
	}
	in.Description = strings.TrimSpace(in.Description)
	fields := map[string]string{}
	if utf8.RuneCountInString(in.Description) > 2000 {
		fields["description"] = "Máximo 2000 caracteres."
	}
	if in.Status != domain.RoomDraft && in.Status != domain.RoomPublished {
		fields["status"] = "Elige si el aula está publicada."
	}
	if len(fields) > 0 {
		return domain.Room{}, domain.Validation(fields)
	}
	if !sc.manager() {
		in.StaffID = room.StaffID
	}
	out, err := s.repo.UpdateRoom(ctx, sc.institution.ID, in)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) && in.StaffID != room.StaffID {
			return domain.Room{}, domain.Validation(map[string]string{"staffId": "Elige a alguien del personal activo."})
		}
		return domain.Room{}, err
	}
	if out.Status != room.Status {
		verb := "Publicó"
		if out.Status == domain.RoomDraft {
			verb = "Ocultó"
		}
		s.audit.Record(ctx, sc.institutionScope, AuditEntry{Module: "classroom", Action: domain.AuditStatus, Entity: "room", EntityID: out.ID,
			Summary: verb + " el aula de " + out.SubjectName + " · " + out.GradeName})
	}
	s.changed(ctx, sc, "room", out.ID)
	return out, nil
}

// ---------- units ----------

func validateUnit(u *domain.Unit) map[string]string {
	fields := map[string]string{}
	u.Title = strings.Join(strings.Fields(u.Title), " ")
	u.Summary = strings.TrimSpace(u.Summary)
	if n := utf8.RuneCountInString(u.Title); n < 2 || n > 160 {
		fields["title"] = "Escribe el título (máximo 160 caracteres)."
	}
	if utf8.RuneCountInString(u.Summary) > 2000 {
		fields["summary"] = "Máximo 2000 caracteres."
	}
	return fields
}

func (s *ClassroomService) SaveUnit(ctx context.Context, token, slug string, in domain.Unit) (domain.Unit, error) {
	if in.ID != "" {
		sc, err := s.scope(ctx, token, slug)
		if err != nil {
			return domain.Unit{}, err
		}
		current, err := s.repo.Unit(ctx, sc.institution.ID, in.ID)
		if err != nil {
			return domain.Unit{}, err
		}
		in.RoomID = current.RoomID
	}
	sc, _, err := s.editable(ctx, token, slug, in.RoomID)
	if err != nil {
		return domain.Unit{}, err
	}
	if fields := validateUnit(&in); len(fields) > 0 {
		return domain.Unit{}, domain.Validation(fields)
	}
	out, err := s.repo.SaveUnit(ctx, sc.institution.ID, in)
	if err != nil {
		return domain.Unit{}, err
	}
	s.changed(ctx, sc, "room", in.RoomID)
	return out, nil
}

// unitRoom resolves the room of a unit and checks the person can edit it
func (s *ClassroomService) unitRoom(ctx context.Context, token, slug, unitID string) (classroomScope, domain.Unit, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return sc, domain.Unit{}, err
	}
	u, err := s.repo.Unit(ctx, sc.institution.ID, unitID)
	if err != nil {
		return sc, u, err
	}
	sc, _, err = s.editable(ctx, token, slug, u.RoomID)
	return sc, u, err
}

func (s *ClassroomService) DeleteUnit(ctx context.Context, token, slug, unitID string) error {
	sc, u, err := s.unitRoom(ctx, token, slug, unitID)
	if err != nil {
		return err
	}
	units, err := s.repo.Units(ctx, u.RoomID, false, "")
	if err != nil {
		return err
	}
	for _, x := range units {
		if x.ID == unitID && len(x.Items) > 0 {
			return domain.ErrUnitHasItems
		}
	}
	if err := s.repo.DeleteUnit(ctx, sc.institution.ID, unitID); err != nil {
		return err
	}
	s.changed(ctx, sc, "room", u.RoomID)
	return nil
}

func (s *ClassroomService) ReorderUnits(ctx context.Context, token, slug, roomID string, ids []string) error {
	sc, _, err := s.editable(ctx, token, slug, roomID)
	if err != nil {
		return err
	}
	if len(ids) > maxUnitsIDs {
		return domain.ErrNotFound
	}
	if err := s.repo.ReorderUnits(ctx, sc.institution.ID, roomID, ids); err != nil {
		return err
	}
	s.changed(ctx, sc, "room", roomID)
	return nil
}

// CopyRoom brings the units and material of another room (a previous period's, usually) into an empty one. Own files
// are not copied: material taken from the library keeps its files
func (s *ClassroomService) CopyRoom(ctx context.Context, token, slug, roomID, fromRoomID string) (int, error) {
	sc, room, err := s.editable(ctx, token, slug, roomID)
	if err != nil {
		return 0, err
	}
	if fromRoomID == roomID {
		return 0, domain.Validation(map[string]string{"fromRoomId": "Elige otra aula."})
	}
	if _, _, _, err := s.roomAccess(ctx, sc, fromRoomID); err != nil {
		return 0, err
	}
	n, err := s.repo.CopyUnits(ctx, sc.institution.ID, fromRoomID, roomID)
	if err != nil {
		return 0, err
	}
	s.audit.Record(ctx, sc.institutionScope, AuditEntry{Module: "classroom", Action: domain.AuditCreate, Entity: "room", EntityID: roomID,
		Summary: "Copió " + itoa(n) + " unidades al aula de " + room.SubjectName + " · " + room.GradeName})
	s.changed(ctx, sc, "room", roomID)
	return n, nil
}

// ---------- items ----------

// validateItem checks the material by kind. Taken from the library, text and link come from there
func validateItem(it *domain.Item) map[string]string {
	fields := map[string]string{}
	it.Title = strings.Join(strings.Fields(it.Title), " ")
	it.Body = strings.TrimSpace(it.Body)
	it.URL = strings.TrimSpace(it.URL)
	it.RecordingURL = strings.TrimSpace(it.RecordingURL)
	if !itemKinds[it.Kind] {
		fields["kind"] = "Elige el tipo de material."
		return fields
	}
	if n := utf8.RuneCountInString(it.Title); n < 2 || n > 160 {
		fields["title"] = "Escribe el título (máximo 160 caracteres)."
	}
	if utf8.RuneCountInString(it.Body) > maxItemBody {
		fields["body"] = "El texto es muy largo (máximo 20 000 caracteres)."
	}
	if it.URL != "" && (!httpsPattern.MatchString(it.URL) || len(it.URL) > 500) {
		fields["url"] = "Escribe una dirección completa que empiece con https://."
	}
	if it.RecordingURL != "" && (!httpsPattern.MatchString(it.RecordingURL) || len(it.RecordingURL) > 500) {
		fields["recordingUrl"] = "Escribe una dirección completa que empiece con https://."
	}
	fromLibrary := it.LibraryItemID != ""
	switch it.Kind {
	case domain.ItemReading, domain.ItemActivity:
		if !fromLibrary && it.Body == "" {
			fields["body"] = "Escribe el contenido o las indicaciones."
		}
	case domain.ItemVideo, domain.ItemLink:
		if !fromLibrary && it.URL == "" && fields["url"] == "" {
			fields["url"] = "Pega la dirección del video o del sitio."
		}
	case domain.ItemLive:
		if it.URL == "" && fields["url"] == "" {
			fields["url"] = "Pega el enlace de la videollamada."
		}
		if it.StartsAt == nil {
			fields["startsAt"] = "Indica el día y la hora de la sesión."
		}
		if it.DurationMinutes == nil || *it.DurationMinutes < 5 || *it.DurationMinutes > 600 {
			fields["durationMinutes"] = "La duración va de 5 a 600 minutos."
		}
	}
	if it.Kind != domain.ItemLive {
		it.StartsAt, it.DurationMinutes, it.RecordingURL = nil, nil, ""
	}
	return fields
}

func (s *ClassroomService) SaveItem(ctx context.Context, token, slug string, in domain.Item) (domain.Item, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.Item{}, err
	}
	if in.ID != "" {
		current, err := s.repo.Item(ctx, sc.institution.ID, in.ID)
		if err != nil {
			return domain.Item{}, err
		}
		in.UnitID, in.LibraryItemID = current.UnitID, current.LibraryItemID
		// An assessment stays an assessment (its questions and attempts hang on it), and nothing becomes one
		special := map[string]bool{domain.ItemQuiz: true, domain.ItemTask: true, domain.ItemForum: true}
		if (special[current.Kind] || special[in.Kind]) && in.Kind != current.Kind {
			return domain.Item{}, domain.Validation(map[string]string{"kind": "Un cuestionario, una tarea o un foro no cambia de tipo."})
		}
	}
	sc, _, err = s.unitRoom(ctx, token, slug, in.UnitID)
	if err != nil {
		return domain.Item{}, err
	}
	// Material taken from the library keeps its kind, and its text and link stay in the library
	if in.LibraryItemID != "" {
		lib, err := s.repo.LibraryItem(ctx, sc.institution.ID, in.LibraryItemID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return domain.Item{}, domain.Validation(map[string]string{"libraryItemId": "Ese recurso ya no está en la biblioteca."})
			}
			return domain.Item{}, err
		}
		in.Kind, in.Body, in.URL = lib.Kind, "", ""
		if strings.TrimSpace(in.Title) == "" {
			in.Title = lib.Title
		}
	}
	if fields := validateItem(&in); len(fields) > 0 {
		return domain.Item{}, domain.Validation(fields)
	}
	out, err := s.repo.SaveItem(ctx, sc.institution.ID, sc.userID, in)
	if err != nil {
		return domain.Item{}, err
	}
	// A new quiz or task starts with its default settings (one attempt, always open)
	if in.ID == "" && (out.Kind == domain.ItemQuiz || out.Kind == domain.ItemTask) {
		if err := s.repo.EnsureAssessment(ctx, out.ID); err != nil {
			return domain.Item{}, err
		}
	}
	// A new forum starts as an open debate
	if in.ID == "" && out.Kind == domain.ItemForum {
		if err := s.repo.EnsureForum(ctx, out.ID); err != nil {
			return domain.Item{}, err
		}
	}
	s.changed(ctx, sc, "room", out.RoomID)
	return out, nil
}

func (s *ClassroomService) itemRoom(ctx context.Context, token, slug, itemID string) (classroomScope, domain.Item, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return sc, domain.Item{}, err
	}
	it, err := s.repo.Item(ctx, sc.institution.ID, itemID)
	if err != nil {
		return sc, it, err
	}
	sc, _, err = s.editable(ctx, token, slug, it.RoomID)
	return sc, it, err
}

// DeleteItem removes material; its own files go first, so no stored file is left without its record
func (s *ClassroomService) DeleteItem(ctx context.Context, token, slug, itemID string) error {
	sc, it, err := s.itemRoom(ctx, token, slug, itemID)
	if err != nil {
		return err
	}
	if it.Files > 0 && it.LibraryItemID == "" {
		return domain.ErrItemHasFiles
	}
	if err := s.repo.DeleteItem(ctx, sc.institution.ID, itemID); err != nil {
		return err
	}
	s.changed(ctx, sc, "room", it.RoomID)
	return nil
}

func (s *ClassroomService) ReorderItems(ctx context.Context, token, slug, unitID string, ids []string) error {
	sc, u, err := s.unitRoom(ctx, token, slug, unitID)
	if err != nil {
		return err
	}
	if len(ids) > maxUnitsIDs {
		return domain.ErrNotFound
	}
	if err := s.repo.ReorderItems(ctx, sc.institution.ID, unitID, ids); err != nil {
		return err
	}
	s.changed(ctx, sc, "room", u.RoomID)
	return nil
}

// RecordView counts a student opening a piece of material (or joining a live session). Only students of the room
// count; anyone else is a no-op, so staff previewing a room never inflate participation
func (s *ClassroomService) RecordView(ctx context.Context, token, slug, itemID string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	if sc.role != "student" {
		return nil
	}
	it, err := s.repo.Item(ctx, sc.institution.ID, itemID)
	if err != nil {
		return err
	}
	_, _, viewer, err := s.roomAccess(ctx, sc, it.RoomID)
	if err != nil {
		return err
	}
	u, err := s.repo.Unit(ctx, sc.institution.ID, it.UnitID)
	if err != nil {
		return err
	}
	if viewer == "" || !it.Published || !u.OpenOn(s.now()) {
		return domain.ErrNotFound
	}
	return s.repo.RecordView(ctx, itemID, viewer, s.now())
}

// ---------- library ----------

// libraryScope: staff who edit rooms also keep the library; readers only read it; families do not browse it
func (s *ClassroomService) libraryScope(ctx context.Context, token, slug string, write bool) (classroomScope, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return sc, err
	}
	if sc.family() {
		return sc, domain.ErrClassroomForbidden
	}
	if write && !sc.manager() && !classroomTeachers[sc.role] {
		return sc, domain.ErrClassroomReadOnly
	}
	return sc, nil
}

// LibraryView is the library and whether the person may add to it
type LibraryView struct {
	Items   []domain.LibraryItem
	CanEdit bool
}

func (s *ClassroomService) Library(ctx context.Context, token, slug string) (LibraryView, error) {
	sc, err := s.libraryScope(ctx, token, slug, false)
	if err != nil {
		return LibraryView{}, err
	}
	items, err := s.repo.Library(ctx, sc.institution.ID)
	if err != nil {
		return LibraryView{}, err
	}
	return LibraryView{Items: items, CanEdit: sc.manager() || classroomTeachers[sc.role]}, nil
}

func validateLibraryItem(l *domain.LibraryItem) map[string]string {
	fields := map[string]string{}
	l.Title = strings.Join(strings.Fields(l.Title), " ")
	l.Topic = strings.Join(strings.Fields(l.Topic), " ")
	l.Description = strings.TrimSpace(l.Description)
	l.Body = strings.TrimSpace(l.Body)
	l.URL = strings.TrimSpace(l.URL)
	if !libraryKinds[l.Kind] {
		fields["kind"] = "Elige el tipo de recurso."
	}
	if n := utf8.RuneCountInString(l.Title); n < 2 || n > 160 {
		fields["title"] = "Escribe el título (máximo 160 caracteres)."
	}
	if utf8.RuneCountInString(l.Topic) > 80 {
		fields["topic"] = "Máximo 80 caracteres."
	}
	if utf8.RuneCountInString(l.Description) > 1000 {
		fields["description"] = "Máximo 1000 caracteres."
	}
	if utf8.RuneCountInString(l.Body) > maxItemBody {
		fields["body"] = "El texto es muy largo (máximo 20 000 caracteres)."
	}
	if l.URL != "" && (!httpsPattern.MatchString(l.URL) || len(l.URL) > 500) {
		fields["url"] = "Escribe una dirección completa que empiece con https://."
	}
	switch l.Kind {
	case domain.ItemReading:
		if l.Body == "" {
			fields["body"] = "Escribe el contenido."
		}
	case domain.ItemVideo, domain.ItemLink:
		if l.URL == "" && fields["url"] == "" {
			fields["url"] = "Pega la dirección."
		}
	}
	return fields
}

func (s *ClassroomService) SaveLibraryItem(ctx context.Context, token, slug string, in domain.LibraryItem) (domain.LibraryItem, error) {
	sc, err := s.libraryScope(ctx, token, slug, true)
	if err != nil {
		return domain.LibraryItem{}, err
	}
	if fields := validateLibraryItem(&in); len(fields) > 0 {
		return domain.LibraryItem{}, domain.Validation(fields)
	}
	out, err := s.repo.SaveLibraryItem(ctx, sc.institution.ID, sc.userID, sc.userName, in)
	if err != nil {
		return domain.LibraryItem{}, err
	}
	s.changed(ctx, sc, "library", "")
	return out, nil
}

func (s *ClassroomService) DeleteLibraryItem(ctx context.Context, token, slug, id string) error {
	sc, err := s.libraryScope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	current, err := s.repo.LibraryItem(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if current.Uses > 0 || current.Files > 0 {
		return domain.ErrLibraryInUse
	}
	if err := s.repo.DeleteLibraryItem(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "library", "")
	return nil
}

// ---------- itinerant deliveries ----------

func (s *ClassroomService) Deliveries(ctx context.Context, token, slug, unitID string) ([]domain.Delivery, error) {
	sc, _, err := s.unitRoom(ctx, token, slug, unitID)
	if err != nil {
		return nil, err
	}
	return s.repo.Deliveries(ctx, sc.institution.ID, unitID)
}

// SaveDelivery records when the printed unit reached the student and when it came back; no delivery date removes it
func (s *ClassroomService) SaveDelivery(ctx context.Context, token, slug string, in domain.Delivery) error {
	sc, u, err := s.unitRoom(ctx, token, slug, in.UnitID)
	if err != nil {
		return err
	}
	in.Notes = strings.TrimSpace(in.Notes)
	fields := map[string]string{}
	if in.DeliveredOn == nil && in.ReturnedOn != nil {
		fields["deliveredOn"] = "Indica cuándo se entregó."
	}
	if in.DeliveredOn != nil && in.ReturnedOn != nil && in.ReturnedOn.Before(*in.DeliveredOn) {
		fields["returnedOn"] = "La devolución no puede ser anterior a la entrega."
	}
	if utf8.RuneCountInString(in.Notes) > 300 {
		fields["notes"] = "Máximo 300 caracteres."
	}
	if len(fields) > 0 {
		return domain.Validation(fields)
	}
	in.RecordedByName = sc.userName
	if err := s.repo.SaveDelivery(ctx, sc.institution.ID, in); err != nil {
		return err
	}
	s.changed(ctx, sc, "deliveries", u.RoomID)
	return nil
}
