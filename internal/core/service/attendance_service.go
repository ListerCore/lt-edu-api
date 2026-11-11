package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	_ "time/tzdata" // America/Lima on hosts without zoneinfo

	"github.com/skip2/go-qrcode"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// EventAttendanceChanged: {institution, area (gate | daily | staff | class | justifications), student?}. The family page
// refetches only when the student is one of its own
const EventAttendanceChanged = "attendance.changed"

// Card payloads: what the QR of an ID card says
const (
	cardPrefixStudent = "JSPH1-E-"
	cardPrefixStaff   = "JSPH1-P-"
)

var (
	// The gate (auxiliares) and the office: scan, mark any section, close the day, review justifications
	attendanceOffice = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true, "secretary": true, "assistant": true}
	// Staff who read everything; tutors also mark their own sections and teachers take their classes
	attendanceStaff = map[string]bool{"teacher": true, "tutor": true, "psychologist": true}
	// The staff day (who of the staff came) is for dirección and the office
	staffAttendanceManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true, "secretary": true, "assistant": true}
	// Class attendance can be taken by coordinación besides the teacher
	classManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true}
	validMarks    = []string{domain.MarkPresent, domain.MarkLate, domain.MarkAbsent, domain.MarkJustified}
	validReasons  = []string{"health", "family", "procedure", "other"}
	limaFallback  = time.FixedZone("PET", -5*60*60)
)

// lima is the time zone every institution works in (Peru has no daylight saving)
func lima() *time.Location {
	if loc, err := time.LoadLocation("America/Lima"); err == nil {
		return loc
	}
	return limaFallback
}

// AttendanceService runs the attendance of phase 5 of docs/plan-academico.md: the ID card at the gate, the day of
// each section and of the staff, attendance per class, justifications from families and the absence alerts
type AttendanceService struct {
	repo     port.AttendanceRepository
	teaching port.TeachingRepository
	staff    port.StaffRepository
	academic *AcademicService
	now      func() time.Time
}

func NewAttendanceService(repo port.AttendanceRepository, teaching port.TeachingRepository, staff port.StaffRepository, academic *AcademicService) *AttendanceService {
	return &AttendanceService{repo: repo, teaching: teaching, staff: staff, academic: academic, now: time.Now}
}

type attendanceScope struct {
	academicScope
	email    string
	userName string
	// The staff record of the signed-in person (empty: not staff) and the sections they tutor
	staffID  string
	tutoring []string
}

func (sc attendanceScope) office() bool { return attendanceOffice[sc.role] }

func (sc attendanceScope) family() bool { return sc.role == "student" || sc.role == "guardian" }

// canMarkSection: the office marks any section, a tutor their own
func (sc attendanceScope) canMarkSection(sectionID string) bool {
	return sc.office() || slices.Contains(sc.tutoring, sectionID)
}

// canReview: justifications are reviewed by the office and by the tutor of the student's section
func (sc attendanceScope) canReview() bool { return sc.office() || len(sc.tutoring) > 0 }

func (s *AttendanceService) scope(ctx context.Context, token, slug string) (attendanceScope, error) {
	isc, err := resolveInstitution(ctx, s.academic.auth, s.academic.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return attendanceScope{}, err
	}
	if !attendanceOffice[isc.role] && !attendanceStaff[isc.role] && isc.role != "student" && isc.role != "guardian" {
		return attendanceScope{}, domain.ErrAttendanceForbidden
	}
	me, err := s.academic.auth.Me(ctx, token)
	if err != nil {
		return attendanceScope{}, err
	}
	sc := attendanceScope{academicScope: academicScope{institutionScope: isc, profile: profileFor(isc.institution.Kind)}, email: me.User.Email, userName: me.User.FullName}
	if !sc.family() && me.User.Email != "" {
		st, err := s.staff.StaffByEmail(ctx, isc.institution.ID, me.User.Email)
		switch {
		case err == nil:
			sc.staffID = st.ID
			if sc.tutoring, err = s.repo.TutorSections(ctx, isc.institution.ID, st.ID); err != nil {
				return attendanceScope{}, err
			}
		case !errors.Is(err, domain.ErrNotFound):
			return attendanceScope{}, err
		}
	}
	return sc, nil
}

func (s *AttendanceService) changed(ctx context.Context, sc attendanceScope, area, studentID string) {
	data := map[string]string{"institution": sc.institution.Slug, "area": area}
	if studentID != "" {
		data["student"] = studentID
	}
	s.academic.live.Notify(ctx, EventAttendanceChanged, data)
}

// today is the calendar day in Lima, as the date columns store it (midnight UTC)
func (s *AttendanceService) today() time.Time {
	n := s.now().In(lima())
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// dayOrToday validates a requested day: empty is today, the future is refused
func (s *AttendanceService) dayOrToday(day *time.Time) (time.Time, error) {
	if day == nil {
		return s.today(), nil
	}
	d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	if d.After(s.today()) {
		return time.Time{}, domain.ErrFutureDay
	}
	return d, nil
}

func isoWeekday(d time.Time) int {
	w := int(d.Weekday())
	if w == 0 {
		return 7
	}
	return w
}

func (s *AttendanceService) settings(ctx context.Context, sc attendanceScope) (domain.AcademicSettings, error) {
	return s.academic.settings(ctx, sc.academicScope)
}

func schoolDay(settings domain.AcademicSettings, d time.Time) bool {
	return slices.Contains(settings.WeekDays, isoWeekday(d))
}

// lateAfter is the local "HH:MM" from which an arrival on that shift counts as late (empty: no class blocks)
func lateAfter(blocks []domain.TimeBlock, shift string, minutes int) string {
	first := ""
	for _, b := range blocks {
		if b.Kind == "class" && (shift == "" || b.Shift == shift) && (first == "" || b.StartsAt < first) {
			first = b.StartsAt
		}
	}
	if first == "" {
		return ""
	}
	t, err := time.Parse("15:04", first)
	if err != nil {
		return ""
	}
	return t.Add(time.Duration(minutes) * time.Minute).Format("15:04")
}

// ---------- overview ----------

type AttendanceOverview struct {
	Day       time.Time
	Today     time.Time
	SchoolDay bool
	Settings  domain.AcademicSettings
	Sections  []domain.SectionDay
	// What the signed-in person can do
	CanScan, CanManageStaff, CanReview, CanTakeClasses bool
	Tutoring                                           []string
	PendingJustifications                              int
	Vocabulary                                         AcademicVocabulary
	StaffTally                                         domain.Tally
	StaffTotal                                         int
}

func (s *AttendanceService) Overview(ctx context.Context, token, slug string, day *time.Time) (AttendanceOverview, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return AttendanceOverview{}, err
	}
	if sc.family() {
		return AttendanceOverview{}, domain.ErrAttendanceForbidden
	}
	d, err := s.dayOrToday(day)
	if err != nil {
		return AttendanceOverview{}, err
	}
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return AttendanceOverview{}, err
	}
	sections, err := s.repo.SectionDays(ctx, sc.institution.ID, d)
	if err != nil {
		return AttendanceOverview{}, err
	}
	out := AttendanceOverview{Day: d, Today: s.today(), SchoolDay: schoolDay(settings, d), Settings: settings, Sections: sections,
		CanScan: sc.office(), CanManageStaff: staffAttendanceManagers[sc.role], CanReview: sc.canReview(),
		CanTakeClasses: sc.staffID != "" || classManagers[sc.role], Tutoring: sc.tutoring, Vocabulary: sc.profile.vocabulary}
	if out.CanReview {
		f := domain.JustificationFilter{InstitutionID: sc.institution.ID, Status: domain.JustificationPending, Limit: 1}
		if !sc.office() {
			f.SectionIDs = sc.tutoring
		}
		if _, out.PendingJustifications, err = s.repo.Justifications(ctx, f); err != nil {
			return out, err
		}
	}
	if out.CanManageStaff {
		rows, err := s.repo.StaffDay(ctx, sc.institution.ID, d)
		if err != nil {
			return out, err
		}
		out.StaffTotal = len(rows)
		for _, r := range rows {
			addTally(&out.StaffTally, r.Status)
		}
	}
	return out, nil
}

func addTally(t *domain.Tally, status string) {
	switch status {
	case domain.MarkPresent:
		t.Present++
	case domain.MarkLate:
		t.Late++
	case domain.MarkAbsent:
		t.Absent++
	case domain.MarkJustified:
		t.Justified++
	}
}

// ---------- gate ----------

// Scan registers an ID card at the gate: the QR payload, or a code / DNI typed by hand. The first arrival of the day
// decides present or late; later reads are logged but keep it
func (s *AttendanceService) Scan(ctx context.Context, token, slug, input string) (domain.GateResult, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.GateResult{}, err
	}
	if !sc.office() {
		return domain.GateResult{}, domain.ErrAttendanceReadOnly
	}
	input = strings.TrimSpace(input)
	if input == "" || len(input) > 80 {
		return domain.GateResult{}, domain.ErrCardUnknown
	}
	now := s.now()
	today := s.today()
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return domain.GateResult{}, err
	}
	if !schoolDay(settings, today) {
		return domain.GateResult{}, domain.ErrNotSchoolDay
	}
	var holder domain.CardHolder
	var status string
	source := "qr"
	upper := strings.ToUpper(input)
	switch {
	case strings.HasPrefix(upper, cardPrefixStudent):
		holder, status, err = s.repo.HolderByToken(ctx, sc.institution.ID, "student", strings.ToLower(input[len(cardPrefixStudent):]), today)
	case strings.HasPrefix(upper, cardPrefixStaff):
		holder, status, err = s.repo.HolderByToken(ctx, sc.institution.ID, "staff", strings.ToLower(input[len(cardPrefixStaff):]), today)
	default:
		source = "manual"
		holder, status, err = s.repo.HolderByCode(ctx, sc.institution.ID, input, today)
	}
	if errors.Is(err, domain.ErrNotFound) {
		return domain.GateResult{}, domain.ErrCardUnknown
	}
	if err != nil {
		return domain.GateResult{}, err
	}
	if status != "active" {
		return domain.GateResult{}, domain.ErrCardInactive
	}
	if holder.Kind == "student" && holder.Shift == "" {
		return domain.GateResult{}, domain.ErrNotEnrolledToday
	}
	if err := s.repo.RecordScan(ctx, sc.institution.ID, holder, now, source, sc.userID); err != nil {
		return domain.GateResult{}, err
	}
	res := domain.GateResult{Holder: holder, ArrivedAt: now}
	current, err := s.repo.DailyMark(ctx, sc.institution.ID, holder.Kind, holder.ID, today)
	switch {
	case err == nil && current.Status != domain.MarkAbsent:
		// Already in: keep the first arrival
		res.Repeated, res.Status = true, current.Status
		if current.ArrivedAt != nil {
			res.ArrivedAt = *current.ArrivedAt
		}
		return res, nil
	case err != nil && !errors.Is(err, domain.ErrNotFound):
		return domain.GateResult{}, err
	}
	blocks, err := s.teaching.TimeBlocks(ctx, sc.institution.ID)
	if err != nil {
		return domain.GateResult{}, err
	}
	res.Status = domain.MarkPresent
	if limit := lateAfter(blocks, holder.Shift, settings.LateAfterMinutes); limit != "" && now.In(lima()).Format("15:04") > limit {
		res.Status = domain.MarkLate
	}
	// Absent after the day was closed and then arriving: it becomes a late arrival
	if err == nil && current.Status == domain.MarkAbsent {
		res.Status = domain.MarkLate
	}
	if err := s.repo.SaveDailyMark(ctx, sc.institution.ID, holder.Kind, domain.DailyMark{PersonID: holder.ID, Day: today, Status: res.Status, ArrivedAt: &now, Source: domain.SourceGate}, sc.userID); err != nil {
		return domain.GateResult{}, err
	}
	if holder.Kind == "staff" {
		s.changed(ctx, sc, "staff", "")
	} else {
		s.changed(ctx, sc, "gate", holder.ID)
	}
	return res, nil
}

func (s *AttendanceService) RecentScans(ctx context.Context, token, slug string, limit int) ([]domain.GateScan, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	if !sc.office() {
		return nil, domain.ErrAttendanceReadOnly
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	return s.repo.RecentScans(ctx, sc.institution.ID, s.today().Add(5*time.Hour), limit)
}

// ---------- day of a section ----------

type SectionRoster struct {
	Day       time.Time
	SchoolDay bool
	Section   domain.SectionDay
	Rows      []domain.RosterRow
	CanEdit   bool
	LateAfter string
}

func (s *AttendanceService) Roster(ctx context.Context, token, slug, sectionID string, day *time.Time) (SectionRoster, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return SectionRoster{}, err
	}
	if sc.family() {
		return SectionRoster{}, domain.ErrAttendanceForbidden
	}
	d, err := s.dayOrToday(day)
	if err != nil {
		return SectionRoster{}, err
	}
	return s.roster(ctx, sc, sectionID, d)
}

func (s *AttendanceService) roster(ctx context.Context, sc attendanceScope, sectionID string, d time.Time) (SectionRoster, error) {
	days, err := s.repo.SectionDays(ctx, sc.institution.ID, d)
	if err != nil {
		return SectionRoster{}, err
	}
	i := slices.IndexFunc(days, func(x domain.SectionDay) bool { return x.SectionID == sectionID })
	if i < 0 {
		return SectionRoster{}, domain.ErrNotFound
	}
	rows, err := s.repo.Roster(ctx, sc.institution.ID, sectionID, d)
	if err != nil {
		return SectionRoster{}, err
	}
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return SectionRoster{}, err
	}
	blocks, err := s.teaching.TimeBlocks(ctx, sc.institution.ID)
	if err != nil {
		return SectionRoster{}, err
	}
	return SectionRoster{Day: d, SchoolDay: schoolDay(settings, d), Section: days[i], Rows: rows, CanEdit: sc.canMarkSection(sectionID),
		LateAfter: lateAfter(blocks, days[i].Shift, settings.LateAfterMinutes)}, nil
}

type MarkInput struct {
	PersonID string
	Status   string
	Note     string
}

// MarkStudents saves the marks of a section by hand (the list of the tutor, or corrections from the office)
func (s *AttendanceService) MarkStudents(ctx context.Context, token, slug, sectionID string, day *time.Time, marks []MarkInput) (SectionRoster, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return SectionRoster{}, err
	}
	if !sc.canMarkSection(sectionID) {
		return SectionRoster{}, domain.ErrAttendanceReadOnly
	}
	d, err := s.dayOrToday(day)
	if err != nil {
		return SectionRoster{}, err
	}
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return SectionRoster{}, err
	}
	if !schoolDay(settings, d) {
		return SectionRoster{}, domain.ErrNotSchoolDay
	}
	rows, err := s.repo.Roster(ctx, sc.institution.ID, sectionID, d)
	if err != nil {
		return SectionRoster{}, err
	}
	if len(rows) == 0 {
		if _, err := s.roster(ctx, sc, sectionID, d); err != nil {
			return SectionRoster{}, err
		}
	}
	current := map[string]domain.RosterRow{}
	for _, r := range rows {
		current[r.StudentID] = r
	}
	fields := map[string]string{}
	for _, m := range marks {
		if _, ok := current[m.PersonID]; !ok {
			fields["marks"] = "Hay estudiantes que no son de esta sección."
		}
		if !slices.Contains(validMarks, m.Status) {
			fields["marks"] = "Elige presente, tardanza, falta o justificada."
		}
		if len([]rune(m.Note)) > 300 {
			fields["marks"] = "Las observaciones tienen hasta 300 caracteres."
		}
	}
	if len(fields) > 0 {
		return SectionRoster{}, domain.Validation(fields)
	}
	for _, m := range marks {
		cur := current[m.PersonID]
		note := strings.TrimSpace(m.Note)
		if cur.Status == m.Status && cur.Note == note {
			continue
		}
		mark := domain.DailyMark{PersonID: m.PersonID, Day: d, Status: m.Status, Source: domain.SourceManual, Note: note, ArrivedAt: cur.ArrivedAt}
		if m.Status == domain.MarkAbsent || m.Status == domain.MarkJustified {
			mark.ArrivedAt = nil
		}
		if err := s.repo.SaveDailyMark(ctx, sc.institution.ID, "student", mark, sc.userID); err != nil {
			return SectionRoster{}, err
		}
		s.changed(ctx, sc, "daily", m.PersonID)
	}
	return s.roster(ctx, sc, sectionID, d)
}

// CloseDay marks absent whoever has no mark yet (one section, or every section for the office)
func (s *AttendanceService) CloseDay(ctx context.Context, token, slug, sectionID string, day *time.Time) (int, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return 0, err
	}
	if sectionID == "" && !sc.office() || sectionID != "" && !sc.canMarkSection(sectionID) {
		return 0, domain.ErrAttendanceReadOnly
	}
	d, err := s.dayOrToday(day)
	if err != nil {
		return 0, err
	}
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return 0, err
	}
	if !schoolDay(settings, d) {
		return 0, domain.ErrNotSchoolDay
	}
	var ids []string
	if sectionID != "" {
		ids = []string{sectionID}
	}
	n, err := s.repo.CloseDay(ctx, sc.institution.ID, d, ids, sc.userID)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		s.changed(ctx, sc, "daily", "")
	}
	return n, nil
}

// ---------- staff day ----------

type StaffDay struct {
	Day       time.Time
	SchoolDay bool
	Rows      []domain.StaffRow
	LateAfter string
}

func (s *AttendanceService) StaffDay(ctx context.Context, token, slug string, day *time.Time) (StaffDay, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return StaffDay{}, err
	}
	if !staffAttendanceManagers[sc.role] {
		return StaffDay{}, domain.ErrAttendanceForbidden
	}
	d, err := s.dayOrToday(day)
	if err != nil {
		return StaffDay{}, err
	}
	return s.staffDay(ctx, sc, d)
}

func (s *AttendanceService) staffDay(ctx context.Context, sc attendanceScope, d time.Time) (StaffDay, error) {
	rows, err := s.repo.StaffDay(ctx, sc.institution.ID, d)
	if err != nil {
		return StaffDay{}, err
	}
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return StaffDay{}, err
	}
	blocks, err := s.teaching.TimeBlocks(ctx, sc.institution.ID)
	if err != nil {
		return StaffDay{}, err
	}
	return StaffDay{Day: d, SchoolDay: schoolDay(settings, d), Rows: rows, LateAfter: lateAfter(blocks, "", settings.LateAfterMinutes)}, nil
}

func (s *AttendanceService) MarkStaff(ctx context.Context, token, slug string, day *time.Time, marks []MarkInput) (StaffDay, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return StaffDay{}, err
	}
	if !staffAttendanceManagers[sc.role] {
		return StaffDay{}, domain.ErrAttendanceReadOnly
	}
	d, err := s.dayOrToday(day)
	if err != nil {
		return StaffDay{}, err
	}
	current, err := s.staffDay(ctx, sc, d)
	if err != nil {
		return StaffDay{}, err
	}
	if !current.SchoolDay {
		return StaffDay{}, domain.ErrNotSchoolDay
	}
	byID := map[string]domain.StaffRow{}
	for _, r := range current.Rows {
		byID[r.StaffID] = r
	}
	for _, m := range marks {
		if _, ok := byID[m.PersonID]; !ok || !slices.Contains(validMarks, m.Status) || len([]rune(m.Note)) > 300 {
			return StaffDay{}, domain.Validation(map[string]string{"marks": "Revisa las marcas del personal."})
		}
	}
	for _, m := range marks {
		cur := byID[m.PersonID]
		note := strings.TrimSpace(m.Note)
		if cur.Status == m.Status && cur.Note == note {
			continue
		}
		mark := domain.DailyMark{PersonID: m.PersonID, Day: d, Status: m.Status, Source: domain.SourceManual, Note: note, ArrivedAt: cur.ArrivedAt}
		if m.Status == domain.MarkAbsent || m.Status == domain.MarkJustified {
			mark.ArrivedAt = nil
		}
		if err := s.repo.SaveDailyMark(ctx, sc.institution.ID, "staff", mark, sc.userID); err != nil {
			return StaffDay{}, err
		}
	}
	s.changed(ctx, sc, "staff", "")
	return s.staffDay(ctx, sc, d)
}

func (s *AttendanceService) CloseStaffDay(ctx context.Context, token, slug string, day *time.Time) (int, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return 0, err
	}
	if !staffAttendanceManagers[sc.role] {
		return 0, domain.ErrAttendanceReadOnly
	}
	d, err := s.dayOrToday(day)
	if err != nil {
		return 0, err
	}
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return 0, err
	}
	if !schoolDay(settings, d) {
		return 0, domain.ErrNotSchoolDay
	}
	n, err := s.repo.CloseStaffDay(ctx, sc.institution.ID, d)
	if err == nil && n > 0 {
		s.changed(ctx, sc, "staff", "")
	}
	return n, err
}

// ---------- classes ----------

type ClassDay struct {
	Day       time.Time
	SchoolDay bool
	Slots     []domain.ClassSlot
	// teacher (own classes) | section (one section's classes)
	Scope string
}

// Classes lists the classes of a day with whether attendance was taken: the teacher's own, or a section's (for
// coordinación and tutors)
func (s *AttendanceService) Classes(ctx context.Context, token, slug, sectionID string, day *time.Time) (ClassDay, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return ClassDay{}, err
	}
	if sc.family() {
		return ClassDay{}, domain.ErrAttendanceForbidden
	}
	d, err := s.dayOrToday(day)
	if err != nil {
		return ClassDay{}, err
	}
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return ClassDay{}, err
	}
	out := ClassDay{Day: d, SchoolDay: schoolDay(settings, d), Scope: "teacher"}
	periods, err := s.academic.repo.Periods(ctx, sc.institution.ID)
	if err != nil {
		return out, err
	}
	var running []string
	for _, p := range periods {
		if p.Status == domain.PeriodActive && !d.Before(p.StartsOn) && !d.After(p.EndsOn) {
			running = append(running, p.ID)
		}
	}
	var entries []domain.ScheduleEntry
	switch {
	case sectionID != "":
		out.Scope = "section"
		if entries, err = s.teaching.SectionEntries(ctx, sc.institution.ID, sectionID); err != nil {
			return out, err
		}
		entries = slices.DeleteFunc(entries, func(e domain.ScheduleEntry) bool { return !slices.Contains(running, e.PeriodID) })
	case sc.staffID != "" && len(running) > 0:
		if entries, err = s.teaching.StaffEntries(ctx, sc.institution.ID, sc.staffID, running); err != nil {
			return out, err
		}
	}
	wd := isoWeekday(d)
	var ids []string
	for _, e := range entries {
		if e.Weekday == wd {
			out.Slots = append(out.Slots, domain.ClassSlot{AssignmentID: e.AssignmentID, SectionID: e.SectionID, SectionLabel: e.SectionLabel, SubjectName: e.SubjectName,
				StaffName: e.StaffName, BlockID: e.BlockID, StartsAt: e.StartsAt, EndsAt: e.EndsAt, Room: e.Room})
			ids = append(ids, e.AssignmentID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	taken, err := s.repo.Sessions(ctx, sc.institution.ID, d, ids)
	if err != nil {
		return out, err
	}
	for i, slot := range out.Slots {
		if t, ok := taken[slot.AssignmentID+"|"+slot.BlockID]; ok {
			out.Slots[i].SessionID, out.Slots[i].Topic, out.Slots[i].Absent, out.Slots[i].Students = t.SessionID, t.Topic, t.Absent, t.Students
		}
	}
	slices.SortFunc(out.Slots, func(a, b domain.ClassSlot) int {
		return strings.Compare(a.StartsAt+a.SectionLabel, b.StartsAt+b.SectionLabel)
	})
	return out, nil
}

type ClassSheet struct {
	Day     time.Time
	Slot    domain.ClassSlot
	Marks   []domain.ClassMark
	CanEdit bool
}

// classSlot checks that the assignment has a class in that block on the weekday of d, and who may take it
func (s *AttendanceService) classSlot(ctx context.Context, sc attendanceScope, assignmentID, blockID string, d time.Time) (domain.Assignment, domain.ClassSlot, bool, error) {
	a, err := s.teaching.Assignment(ctx, sc.institution.ID, assignmentID)
	if err != nil {
		return domain.Assignment{}, domain.ClassSlot{}, false, err
	}
	entries, err := s.teaching.SectionEntries(ctx, sc.institution.ID, a.SectionID)
	if err != nil {
		return a, domain.ClassSlot{}, false, err
	}
	wd := isoWeekday(d)
	i := slices.IndexFunc(entries, func(e domain.ScheduleEntry) bool {
		return e.AssignmentID == assignmentID && e.BlockID == blockID && e.Weekday == wd
	})
	if i < 0 {
		return a, domain.ClassSlot{}, false, domain.ErrNoSuchClass
	}
	e := entries[i]
	slot := domain.ClassSlot{AssignmentID: a.ID, SectionID: a.SectionID, SectionLabel: e.SectionLabel, SubjectName: e.SubjectName, StaffName: e.StaffName,
		BlockID: blockID, StartsAt: e.StartsAt, EndsAt: e.EndsAt, Room: e.Room}
	canEdit := classManagers[sc.role] || (sc.staffID != "" && sc.staffID == a.StaffID)
	return a, slot, canEdit, nil
}

func (s *AttendanceService) Class(ctx context.Context, token, slug, assignmentID, blockID string, day *time.Time) (ClassSheet, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return ClassSheet{}, err
	}
	if sc.family() {
		return ClassSheet{}, domain.ErrAttendanceForbidden
	}
	d, err := s.dayOrToday(day)
	if err != nil {
		return ClassSheet{}, err
	}
	return s.class(ctx, sc, assignmentID, blockID, d)
}

func (s *AttendanceService) class(ctx context.Context, sc attendanceScope, assignmentID, blockID string, d time.Time) (ClassSheet, error) {
	a, slot, canEdit, err := s.classSlot(ctx, sc, assignmentID, blockID, d)
	if err != nil {
		return ClassSheet{}, err
	}
	taken, err := s.repo.Sessions(ctx, sc.institution.ID, d, []string{assignmentID})
	if err != nil {
		return ClassSheet{}, err
	}
	if t, ok := taken[assignmentID+"|"+blockID]; ok {
		slot.SessionID, slot.Topic, slot.Absent, slot.Students = t.SessionID, t.Topic, t.Absent, t.Students
	}
	marks, err := s.repo.ClassRoster(ctx, sc.institution.ID, a.SectionID, a.SubjectID, slot.SessionID, d)
	if err != nil {
		return ClassSheet{}, err
	}
	return ClassSheet{Day: d, Slot: slot, Marks: marks, CanEdit: canEdit}, nil
}

// SaveClass stores the attendance of one class; students left out keep the mark suggested by their day
func (s *AttendanceService) SaveClass(ctx context.Context, token, slug, assignmentID, blockID string, day *time.Time, topic string, marks []MarkInput) (ClassSheet, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return ClassSheet{}, err
	}
	d, err := s.dayOrToday(day)
	if err != nil {
		return ClassSheet{}, err
	}
	sheet, err := s.class(ctx, sc, assignmentID, blockID, d)
	if err != nil {
		return ClassSheet{}, err
	}
	if !sheet.CanEdit {
		return ClassSheet{}, domain.ErrNotYourClass
	}
	topic = strings.TrimSpace(topic)
	if len([]rune(topic)) > 200 {
		return ClassSheet{}, domain.Validation(map[string]string{"topic": "El tema tiene hasta 200 caracteres."})
	}
	given := map[string]MarkInput{}
	for _, m := range marks {
		if !slices.Contains(validMarks, m.Status) || len([]rune(m.Note)) > 200 {
			return ClassSheet{}, domain.Validation(map[string]string{"marks": "Elige presente, tardanza, falta o justificada."})
		}
		given[m.PersonID] = m
	}
	out := make([]domain.ClassMark, 0, len(sheet.Marks))
	for _, row := range sheet.Marks {
		m, ok := given[row.StudentID]
		status, note := suggestedClassMark(row), row.Note
		if ok {
			status, note = m.Status, strings.TrimSpace(m.Note)
		}
		out = append(out, domain.ClassMark{StudentID: row.StudentID, Status: status, Note: note})
	}
	if len(given) > len(out) {
		return ClassSheet{}, domain.Validation(map[string]string{"marks": "Hay estudiantes que no llevan este curso."})
	}
	if _, err := s.repo.SaveSession(ctx, sc.institution.ID, domain.ClassSession{AssignmentID: assignmentID, SectionID: sheet.Slot.SectionID, Day: d, BlockID: blockID, Topic: topic, TakenBy: sc.userID}, out); err != nil {
		return ClassSheet{}, err
	}
	s.changed(ctx, sc, "class", "")
	return s.class(ctx, sc, assignmentID, blockID, d)
}

// suggestedClassMark: the class mark already taken, otherwise what the day says (absent at the gate, absent here)
func suggestedClassMark(row domain.ClassMark) string {
	if row.Status != "" {
		return row.Status
	}
	switch row.DayStatus {
	case domain.MarkAbsent, domain.MarkJustified:
		return row.DayStatus
	}
	return domain.MarkPresent
}

// ---------- justifications ----------

type JustificationPage struct {
	Items     []domain.Justification
	Total     int
	CanReview bool
}

func (s *AttendanceService) Justifications(ctx context.Context, token, slug, status, studentID string, limit, offset int) (JustificationPage, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return JustificationPage{}, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	f := domain.JustificationFilter{InstitutionID: sc.institution.ID, Status: status, StudentID: studentID, Limit: limit, Offset: max(offset, 0)}
	if sc.family() {
		mine, err := s.ownStudents(ctx, sc)
		if err != nil {
			return JustificationPage{}, err
		}
		if studentID == "" || !slices.ContainsFunc(mine, func(h domain.CardHolder) bool { return h.ID == studentID }) {
			return JustificationPage{}, domain.ErrNotYourStudent
		}
	} else if !sc.office() && !attendanceStaff[sc.role] {
		return JustificationPage{}, domain.ErrAttendanceForbidden
	} else if !sc.office() && studentID == "" {
		// Tutors see the students of their sections; other teachers only through a student's page
		if len(sc.tutoring) == 0 {
			return JustificationPage{}, nil
		}
		f.SectionIDs = sc.tutoring
	}
	items, total, err := s.repo.Justifications(ctx, f)
	if err != nil {
		return JustificationPage{}, err
	}
	return JustificationPage{Items: items, Total: total, CanReview: sc.canReview()}, nil
}

func (s *AttendanceService) ownStudents(ctx context.Context, sc attendanceScope) ([]domain.CardHolder, error) {
	if sc.email == "" {
		return nil, nil
	}
	return s.repo.StudentsForEmail(ctx, sc.institution.ID, sc.email)
}

type JustificationInput struct {
	StudentID   string
	FromDay     time.Time
	ToDay       time.Time
	Reason      string
	Description string
}

// SubmitJustification: families justify their children (adult students themselves); the office can register one a
// family brought on paper. It stays pending until someone reviews it
func (s *AttendanceService) SubmitJustification(ctx context.Context, token, slug string, in JustificationInput) (domain.Justification, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.Justification{}, err
	}
	switch {
	case sc.family():
		mine, err := s.ownStudents(ctx, sc)
		if err != nil {
			return domain.Justification{}, err
		}
		if !slices.ContainsFunc(mine, func(h domain.CardHolder) bool { return h.ID == in.StudentID }) {
			return domain.Justification{}, domain.ErrNotYourStudent
		}
	case !sc.canReview():
		return domain.Justification{}, domain.ErrAttendanceReadOnly
	}
	fields := map[string]string{}
	from, to := calendarDay(in.FromDay), calendarDay(in.ToDay)
	switch {
	case in.FromDay.IsZero():
		fields["fromDay"] = "Indica desde qué día."
	case in.ToDay.IsZero():
		fields["toDay"] = "Indica hasta qué día."
	case to.Before(from):
		fields["toDay"] = "La fecha final no puede ser antes de la inicial."
	case to.Sub(from) > 60*24*time.Hour:
		fields["toDay"] = "Una justificación cubre hasta 60 días."
	case from.After(s.today().AddDate(0, 0, 30)):
		fields["fromDay"] = "Solo se puede justificar con hasta 30 días de anticipación."
	case from.Before(s.today().AddDate(0, 0, -60)):
		fields["fromDay"] = "Solo se pueden justificar faltas de los últimos 60 días."
	}
	if !slices.Contains(validReasons, in.Reason) {
		fields["reason"] = "Elige un motivo."
	}
	desc := strings.TrimSpace(in.Description)
	if desc == "" {
		fields["description"] = "Cuéntanos brevemente qué pasó."
	} else if len([]rune(desc)) > 1000 {
		fields["description"] = "La descripción tiene hasta 1000 caracteres."
	}
	if len(fields) > 0 {
		return domain.Justification{}, domain.Validation(fields)
	}
	if !sc.family() {
		if _, _, _, _, err := s.repo.StudentPeriod(ctx, sc.institution.ID, in.StudentID); err != nil {
			return domain.Justification{}, err
		}
	}
	name := sc.userName
	if name == "" {
		name = sc.email
	}
	j, err := s.repo.CreateJustification(ctx, sc.institution.ID, domain.Justification{StudentID: in.StudentID, FromDay: from, ToDay: to, Reason: in.Reason, Description: desc, SubmittedName: name}, sc.userID)
	if err != nil {
		return domain.Justification{}, err
	}
	s.changed(ctx, sc, "justifications", in.StudentID)
	return j, nil
}

func (s *AttendanceService) ReviewJustification(ctx context.Context, token, slug, id string, approve bool, note string) (domain.Justification, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.Justification{}, err
	}
	if !sc.canReview() {
		return domain.Justification{}, domain.ErrAttendanceReadOnly
	}
	j, err := s.repo.Justification(ctx, sc.institution.ID, id)
	if err != nil {
		return domain.Justification{}, err
	}
	if !sc.office() {
		_, section, _, _, err := s.repo.StudentPeriod(ctx, sc.institution.ID, j.StudentID)
		if err != nil {
			return domain.Justification{}, err
		}
		if !slices.Contains(sc.tutoring, section) {
			return domain.Justification{}, domain.ErrAttendanceReadOnly
		}
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 500 {
		return domain.Justification{}, domain.Validation(map[string]string{"note": "La respuesta tiene hasta 500 caracteres."})
	}
	status := domain.JustificationRejected
	if approve {
		status = domain.JustificationApproved
	} else if note == "" {
		return domain.Justification{}, domain.Validation(map[string]string{"note": "Explica a la familia por qué no se acepta."})
	}
	name := sc.userName
	if name == "" {
		name = sc.email
	}
	out, err := s.repo.ReviewJustification(ctx, sc.institution.ID, id, status, note, sc.userID, name)
	if err != nil {
		return domain.Justification{}, err
	}
	s.changed(ctx, sc, "justifications", j.StudentID)
	return out, nil
}

// ---------- reports ----------

type AttendanceReport struct {
	PeriodID string
	Limit    int
	Students []domain.StudentTally
	// Classes per subject of the students at or above the warning line (institutes: the 30 % rule)
	Subjects []domain.SubjectTally
}

// warnAt is where a student starts to show in alerts: three quarters of the limit
func warnAt(limit int) float64 { return float64(limit) * 0.75 }

// minClassesForAlert is how many classes of a subject a student needs before its share raises an alert
const minClassesForAlert = 4

func (s *AttendanceService) Report(ctx context.Context, token, slug, periodID, sectionID string) (AttendanceReport, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return AttendanceReport{}, err
	}
	if sc.family() {
		return AttendanceReport{}, domain.ErrAttendanceForbidden
	}
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return AttendanceReport{}, err
	}
	if periodID == "" {
		periods, err := s.academic.repo.Periods(ctx, sc.institution.ID)
		if err != nil {
			return AttendanceReport{}, err
		}
		today := s.today()
		for _, p := range periods {
			if p.Status == domain.PeriodActive && !today.Before(p.StartsOn) && !today.After(p.EndsOn) {
				periodID = p.ID
				break
			}
		}
		if periodID == "" {
			return AttendanceReport{Limit: settings.AbsenceLimitPercent}, nil
		}
	}
	students, err := s.repo.StudentTallies(ctx, sc.institution.ID, periodID, sectionID)
	if err != nil {
		return AttendanceReport{}, err
	}
	subjects, err := s.repo.SubjectTallies(ctx, sc.institution.ID, periodID, sectionID, "")
	if err != nil {
		return AttendanceReport{}, err
	}
	warn := warnAt(settings.AbsenceLimitPercent)
	// A share needs a few classes to mean something: 1 absence out of 1 class is not an alert yet
	subjects = slices.DeleteFunc(subjects, func(t domain.SubjectTally) bool {
		return t.Classes.Total() < minClassesForAlert || t.Classes.AbsentPercent() < warn
	})
	return AttendanceReport{PeriodID: periodID, Limit: settings.AbsenceLimitPercent, Students: students, Subjects: subjects}, nil
}

// StudentAttendance is one student's record: the period totals, the days that were not plain present, classes per
// subject and justifications
type StudentAttendance struct {
	Student       domain.CardHolder
	SectionLabel  string
	Limit         int
	Today         *domain.DailyMark
	Days          domain.Tally
	Marks         []domain.DailyMark
	Subjects      []domain.SubjectTally
	Justification []domain.Justification
}

func (s *AttendanceService) Student(ctx context.Context, token, slug, studentID string) (StudentAttendance, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return StudentAttendance{}, err
	}
	if sc.family() {
		mine, err := s.ownStudents(ctx, sc)
		if err != nil {
			return StudentAttendance{}, err
		}
		i := slices.IndexFunc(mine, func(h domain.CardHolder) bool { return h.ID == studentID })
		if i < 0 {
			return StudentAttendance{}, domain.ErrNotYourStudent
		}
		return s.student(ctx, sc, mine[i])
	}
	return s.student(ctx, sc, domain.CardHolder{Kind: "student", ID: studentID})
}

func (s *AttendanceService) student(ctx context.Context, sc attendanceScope, h domain.CardHolder) (StudentAttendance, error) {
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return StudentAttendance{}, err
	}
	periodID, _, label, name, err := s.repo.StudentPeriod(ctx, sc.institution.ID, h.ID)
	if err != nil {
		return StudentAttendance{}, err
	}
	if h.Name == "" {
		h.Name = name
	}
	p, err := s.academic.repo.Period(ctx, sc.institution.ID, periodID)
	if err != nil {
		return StudentAttendance{}, err
	}
	out := StudentAttendance{Student: h, SectionLabel: label, Limit: settings.AbsenceLimitPercent}
	marks, err := s.repo.DailyMarks(ctx, sc.institution.ID, h.ID, p.StartsOn, p.EndsOn)
	if err != nil {
		return out, err
	}
	today := s.today()
	for i, m := range marks {
		addTally(&out.Days, m.Status)
		if m.Day.Equal(today) {
			out.Today = &marks[i]
		}
		if m.Status != domain.MarkPresent && len(out.Marks) < 60 {
			out.Marks = append(out.Marks, m)
		}
	}
	if out.Subjects, err = s.repo.SubjectTallies(ctx, sc.institution.ID, periodID, "", h.ID); err != nil {
		return out, err
	}
	js, _, err := s.repo.Justifications(ctx, domain.JustificationFilter{InstitutionID: sc.institution.ID, StudentID: h.ID, Limit: 20})
	if err != nil {
		return out, err
	}
	out.Justification = js
	return out, nil
}

// MyAttendance is the family view: every child (or the adult student) with today's arrival and the period record
func (s *AttendanceService) Mine(ctx context.Context, token, slug string) ([]StudentAttendance, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	if !sc.family() {
		return nil, nil
	}
	mine, err := s.ownStudents(ctx, sc)
	if err != nil {
		return nil, err
	}
	var out []StudentAttendance
	for _, h := range mine {
		a, err := s.student(ctx, sc, h)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// ---------- ID card codes ----------

// CardCode is what the card prints: the payload and its QR as rows of 0/1 (no quiet zone)
type CardCode struct {
	ID      string
	Payload string
	Size    int
	Modules string
}

func cardCode(kind, id, token string) (CardCode, error) {
	prefix := cardPrefixStudent
	if kind == "staff" {
		prefix = cardPrefixStaff
	}
	payload := prefix + strings.ToUpper(token)
	q, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return CardCode{}, err
	}
	q.DisableBorder = true
	bits := q.Bitmap()
	var b strings.Builder
	b.Grow(len(bits) * len(bits))
	for _, row := range bits {
		for _, on := range row {
			if on {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
	}
	return CardCode{ID: id, Payload: payload, Size: len(bits), Modules: b.String()}, nil
}

// CardCodes returns the QR of some ID cards: the office prints anyone's; families and staff only their own
func (s *AttendanceService) CardCodes(ctx context.Context, token, slug, kind string, ids []string) ([]CardCode, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	if kind != "student" && kind != "staff" {
		return nil, domain.Validation(map[string]string{"kind": "Elige estudiantes o personal."})
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 200 {
		ids = ids[:200]
	}
	if !sc.office() {
		var allowed []string
		if kind == "staff" && sc.staffID != "" {
			allowed = []string{sc.staffID}
		}
		if kind == "student" && sc.family() {
			mine, err := s.ownStudents(ctx, sc)
			if err != nil {
				return nil, err
			}
			for _, h := range mine {
				allowed = append(allowed, h.ID)
			}
		}
		ids = slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return !slices.Contains(allowed, id) })
		if len(ids) == 0 {
			return nil, domain.ErrAttendanceForbidden
		}
	}
	tokens, err := s.repo.CardTokens(ctx, sc.institution.ID, kind, ids)
	if err != nil {
		return nil, err
	}
	out := make([]CardCode, 0, len(ids))
	for _, id := range ids {
		t, ok := tokens[id]
		if !ok {
			continue
		}
		c, err := cardCode(kind, id, t)
		if err != nil {
			return nil, fmt.Errorf("card qr: %w", err)
		}
		out = append(out, c)
	}
	return out, nil
}

// RenewCard replaces the secret of a lost card: the old printed QR stops working
func (s *AttendanceService) RenewCard(ctx context.Context, token, slug, kind, id string) (CardCode, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return CardCode{}, err
	}
	if !sc.office() {
		return CardCode{}, domain.ErrAttendanceReadOnly
	}
	if kind != "student" && kind != "staff" {
		return CardCode{}, domain.Validation(map[string]string{"kind": "Elige estudiantes o personal."})
	}
	t, err := s.repo.RenewCardToken(ctx, sc.institution.ID, kind, id)
	if err != nil {
		return CardCode{}, err
	}
	return cardCode(kind, id, t)
}

// calendarDay keeps the date only, at midnight UTC like the date columns
func calendarDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
