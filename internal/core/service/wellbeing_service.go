package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// EventWellbeingChanged: {institution, area (tutoring | psychology | discipline | plans | therapies), student}. Psychology
// events carry no student: the cases are confidential
const EventWellbeingChanged = "wellbeing.changed"

// RoleSpecialEd is the therapist or SAANEE / PRITE professional of special education
const RoleSpecialEd = "special_ed_specialist"

var (
	// Dirección and coordinación see every student and run tutoring
	wellOffice = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true}
	// Serious faults, suspensions, conditional enrollment and expulsion are for the director (DS 021-2026-MINEDU)
	wellDirectors = map[string]bool{domain.RoleSuperadmin: true, "director": true}
	// Staff who see the students of their sections (tutors and teachers) or of the whole school (auxiliares)
	wellStaff = map[string]bool{"teacher": true, "tutor": true, "assistant": true, "psychologist": true, RoleSpecialEd: true}
)

// WellbeingService runs phase 11 of docs/plan-academico.md: tutoring, psychology (confidential), school discipline
// (DS 021-2026-MINEDU) and, in special education, the individual plan (POI) and therapies
type WellbeingService struct {
	repo       port.WellbeingRepository
	attendance *AttendanceService
	// Dropout alerts of the distance form (phase E5); set by the server
	participation *ParticipationService
	// Virtual tutoring (phase E6); set by the server
	tutoring port.TutoringRepository
}

// SetParticipation adds the dropout alerts of the distance students to the inbox
func (s *WellbeingService) SetParticipation(p *ParticipationService) { s.participation = p }

func NewWellbeingService(repo port.WellbeingRepository, attendance *AttendanceService) *WellbeingService {
	return &WellbeingService{repo: repo, attendance: attendance}
}

type wellScope struct {
	attendanceScope
	// Sections the person teaches in active periods
	teaching []string
	// Families: their children
	children []string
	// Specialists: students whose POI they lead or whose therapies they log
	assigned []string
	// Students assigned one by one to this tutor (virtual tutoring, phase E6)
	tutees []string
	// The name on the staff record (therapy sessions are signed with it)
	staffName string
}

func (sc wellScope) office() bool       { return wellOffice[sc.role] }
func (sc wellScope) director() bool     { return wellDirectors[sc.role] }
func (sc wellScope) psychologist() bool { return sc.role == "psychologist" }
func (sc wellScope) specialist() bool   { return sc.role == RoleSpecialEd }
func (sc wellScope) ebe() bool          { return sc.institution.Kind == domain.KindEBE }

// seesAll: dirección, coordinación, psychology and the auxiliares (who report faults anywhere) see every student
func (sc wellScope) seesAll() bool { return sc.office() || sc.psychologist() || sc.role == "assistant" }

func (s *WellbeingService) scope(ctx context.Context, token, slug string) (wellScope, error) {
	isc, err := resolveInstitution(ctx, s.attendance.academic.auth, s.attendance.academic.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return wellScope{}, err
	}
	family := isc.role == "student" || isc.role == "guardian"
	if !wellOffice[isc.role] && !wellStaff[isc.role] && !family {
		return wellScope{}, domain.ErrWellbeingForbidden
	}
	me, err := s.attendance.academic.auth.Me(ctx, token)
	if err != nil {
		return wellScope{}, err
	}
	sc := wellScope{attendanceScope: attendanceScope{academicScope: academicScope{institutionScope: isc, profile: profileFor(isc.institution.Kind)}, email: me.User.Email, userName: me.User.FullName}}
	if family {
		mine, err := s.attendance.ownStudents(ctx, sc.attendanceScope)
		if err != nil {
			return sc, err
		}
		sc.children = []string{}
		for _, h := range mine {
			sc.children = append(sc.children, h.ID)
		}
		return sc, nil
	}
	if me.User.Email == "" {
		return sc, nil
	}
	st, err := s.attendance.staff.StaffByEmail(ctx, isc.institution.ID, me.User.Email)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return sc, nil
	case err != nil:
		return sc, err
	}
	sc.staffID, sc.staffName = st.ID, st.GivenNames+" "+st.Surnames
	if sc.tutoring, err = s.attendance.repo.TutorSections(ctx, isc.institution.ID, st.ID); err != nil {
		return sc, err
	}
	if sc.teaching, err = s.repo.TeacherSections(ctx, isc.institution.ID, st.ID); err != nil {
		return sc, err
	}
	if s.tutoring != nil {
		if sc.tutees, err = s.tutoring.IndividualTutees(ctx, isc.institution.ID, st.ID); err != nil {
			return sc, err
		}
	}
	if sc.specialist() {
		plans, err := s.repo.Plans(ctx, isc.institution.ID, "", st.ID, nil)
		if err != nil {
			return sc, err
		}
		sc.assigned = []string{}
		for _, p := range plans {
			if !slices.Contains(sc.assigned, p.StudentID) {
				sc.assigned = append(sc.assigned, p.StudentID)
			}
		}
	}
	return sc, nil
}

func (s *WellbeingService) changed(ctx context.Context, sc wellScope, area, studentID string) {
	s.attendance.academic.live.Notify(ctx, EventWellbeingChanged, map[string]string{"institution": sc.institution.Slug, "area": area, "student": studentID})
}

func (s *WellbeingService) today() time.Time { return s.attendance.today() }

func (sc wellScope) name() string {
	if sc.userName != "" {
		return sc.userName
	}
	return sc.email
}

// filter is who the person may list: nil slices mean every student
func (sc wellScope) filter() port.WellStudentFilter {
	f := port.WellStudentFilter{InstitutionID: sc.institution.ID}
	switch {
	case sc.seesAll():
	case sc.family():
		f.StudentIDs = sc.children
	case sc.specialist():
		f.StudentIDs = sc.assigned
	default:
		f.SectionIDs = union(sc.tutoring, sc.teaching)
	}
	return f
}

func union(a, b []string) []string {
	out := append([]string{}, a...)
	for _, x := range b {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// student loads one student the person may see
func (s *WellbeingService) student(ctx context.Context, sc wellScope, studentID string) (domain.WellStudent, error) {
	f := sc.filter()
	f.StudentIDs = []string{studentID}
	if !sc.seesAll() && sc.family() && !slices.Contains(sc.children, studentID) {
		return domain.WellStudent{}, domain.ErrNotYourStudent
	}
	if sc.specialist() && !slices.Contains(sc.assigned, studentID) {
		return domain.WellStudent{}, domain.ErrNotYourPlan
	}
	if slices.Contains(sc.tutees, studentID) {
		// A student of their own, maybe from a section they do not tutor
		f.SectionIDs = nil
	}
	list, err := s.repo.Students(ctx, f)
	if err != nil {
		return domain.WellStudent{}, err
	}
	if len(list) == 0 {
		if sc.seesAll() {
			return domain.WellStudent{}, domain.ErrNotFound
		}
		return domain.WellStudent{}, domain.ErrNotYourStudent
	}
	return list[0], nil
}

// tutors reports whether the person runs the tutoring of the student (office or the tutor of their section)
func (sc wellScope) tutors(st domain.WellStudent) bool {
	return sc.office() || slices.Contains(sc.tutoring, st.SectionID) || slices.Contains(sc.tutees, st.ID)
}

// readsTutoring: the office, psychology and the tutor of the section read interviews and commitments
func (sc wellScope) readsTutoring(st domain.WellStudent) bool {
	return sc.tutors(st) || sc.psychologist()
}

func tooLong(v string, n int) bool { return utf8.RuneCountInString(v) > n }

// ---------- students ----------

type WellStudents struct {
	Students []domain.WellStudent
	// What the screen may offer
	CanTutor  bool
	CanReport bool
	CanDecide bool
	CanPlan   bool
	Family    bool
	EBE       bool
}

func (s *WellbeingService) Students(ctx context.Context, token, slug, query string, specialOnly bool) (WellStudents, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return WellStudents{}, err
	}
	f := sc.filter()
	f.Query = strings.TrimSpace(query)
	f.SpecialNeeds = specialOnly
	list, err := s.repo.Students(ctx, f)
	if err != nil {
		return WellStudents{}, err
	}
	return WellStudents{
		Students: list, CanTutor: sc.office() || len(sc.tutoring) > 0, CanReport: !sc.family(), CanDecide: sc.director(),
		CanPlan: sc.ebe() && (sc.office() || sc.specialist()), Family: sc.family(), EBE: sc.ebe(),
	}, nil
}

// StudentFile is everything wellbeing knows of a student that the person may read
type StudentFile struct {
	Student     domain.WellStudent
	Interviews  []domain.Interview
	Commitments []domain.Commitment
	Incidents   []domain.Incident
	// Measures not tied to an incident (conditional enrollment, expulsion) and every measure of the year
	Measures  []domain.Measure
	Conduct   []domain.TermConduct
	Cases     []domain.PsychCase
	Plans     []domain.IndividualPlan
	Therapies []domain.TherapySession
	// Virtual tutoring (phase E6): the sessions the student was called to and the SAANEE referrals
	Sessions []domain.TutoringSession
	Saanee   []domain.SaaneeReferral
	// What the person may do here
	CanTutor  bool
	CanRefer  bool
	CanReport bool
	CanDecide bool
	CanPlan   bool
	Family    bool
	Today     time.Time
}

func (s *WellbeingService) File(ctx context.Context, token, slug, studentID string) (StudentFile, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return StudentFile{}, err
	}
	st, err := s.student(ctx, sc, studentID)
	if err != nil {
		return StudentFile{}, err
	}
	inst := sc.institution.ID
	ids := []string{st.ID}
	file := StudentFile{Student: st, Today: s.today(), Family: sc.family(), CanTutor: sc.tutors(st), CanReport: !sc.family(), CanDecide: sc.director(),
		CanRefer: !sc.specialist(), CanPlan: sc.ebe() && (sc.office() || (sc.specialist() && slices.Contains(sc.assigned, st.ID)))}

	// Tutoring: staff who run or support it read everything; families only the family interviews (without the
	// tutor's notes) and the commitments
	if sc.readsTutoring(st) || sc.family() {
		if file.Interviews, err = s.repo.Interviews(ctx, inst, ids, nil); err != nil {
			return file, err
		}
		if file.Commitments, err = s.repo.Commitments(ctx, inst, ids, false); err != nil {
			return file, err
		}
		if sc.family() {
			file.Interviews = slices.DeleteFunc(file.Interviews, func(x domain.Interview) bool { return x.Kind != "family" })
			for i := range file.Interviews {
				file.Interviews[i].Notes = ""
			}
		}
	}

	// Virtual tutoring: the sessions and the SAANEE referrals, for whoever reads the tutoring
	if s.tutoring != nil && sc.readsTutoring(st) {
		if file.Sessions, err = s.tutoring.Sessions(ctx, domain.SessionFilter{InstitutionID: inst, StudentID: st.ID}); err != nil {
			return file, err
		}
		if file.Saanee, err = s.tutoring.SaaneeReferrals(ctx, inst, ids); err != nil {
			return file, err
		}
	}

	// Discipline and conduct: every role that sees the student
	if file.Incidents, err = s.repo.Incidents(ctx, domain.IncidentFilter{InstitutionID: inst, StudentIDs: ids}); err != nil {
		return file, err
	}
	markMine(sc, file.Incidents)
	yearStart := time.Date(file.Today.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	if file.Measures, err = s.repo.Measures(ctx, inst, st.ID, yearStart); err != nil {
		return file, err
	}
	periodID, _, err := s.repo.CurrentPeriod(ctx, inst, file.Today)
	if err != nil {
		return file, err
	}
	if periodID != "" {
		if file.Conduct, err = s.repo.Conduct(ctx, inst, st.ID, periodID); err != nil {
			return file, err
		}
	}

	// Psychology: psychologists read the cases; whoever referred reads the status of their referrals
	switch {
	case sc.psychologist():
		file.Cases, err = s.repo.Cases(ctx, inst, ids, "", "")
	case !sc.family():
		file.Cases, err = s.repo.Cases(ctx, inst, ids, "", sc.userID)
		for i := range file.Cases {
			file.Cases[i] = referrerView(file.Cases[i])
		}
	}
	if err != nil {
		return file, err
	}

	// Special education: the POI and the therapies (families read the approved plan and no professional notes)
	if sc.ebe() {
		if file.Plans, err = s.repo.Plans(ctx, inst, "", "", ids); err != nil {
			return file, err
		}
		if file.Therapies, err = s.repo.Therapies(ctx, port.TherapyFilter{InstitutionID: inst, StudentIDs: ids}); err != nil {
			return file, err
		}
		if sc.family() {
			file.Plans = slices.DeleteFunc(file.Plans, func(p domain.IndividualPlan) bool { return p.Status != "approved" })
			for i := range file.Therapies {
				file.Therapies[i].Notes = ""
			}
		}
		for i := range file.Therapies {
			file.Therapies[i].Mine = sc.staffID != "" && file.Therapies[i].TherapistID == sc.staffID
		}
	}
	return file, nil
}

// referrerView is a case as whoever referred it sees it: status and the note the psychologist shares, no sessions
func referrerView(c domain.PsychCase) domain.PsychCase {
	c.Sessions, c.LastSession = 0, nil
	return c
}

// ---------- tutoring ----------

func (s *WellbeingService) SaveInterview(ctx context.Context, token, slug string, x domain.Interview) (domain.Interview, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.Interview{}, err
	}
	if x.ID != "" {
		old, err := s.repo.Interview(ctx, sc.institution.ID, x.ID)
		if err != nil {
			return domain.Interview{}, err
		}
		x.StudentID = old.StudentID
	}
	st, err := s.student(ctx, sc, x.StudentID)
	if err != nil {
		return domain.Interview{}, err
	}
	if !sc.tutors(st) {
		return domain.Interview{}, domain.ErrNotYourTutee
	}
	x.Topic, x.Notes, x.Agreements = strings.TrimSpace(x.Topic), strings.TrimSpace(x.Notes), strings.TrimSpace(x.Agreements)
	fields := map[string]string{}
	if x.Kind != "student" && x.Kind != "family" {
		fields["kind"] = "Elige si fue con el estudiante o con la familia."
	}
	if x.Topic == "" || tooLong(x.Topic, 160) {
		fields["topic"] = "Escribe el motivo (hasta 160 caracteres)."
	}
	if tooLong(x.Notes, 4000) {
		fields["notes"] = "Las notas admiten hasta 4000 caracteres."
	}
	if tooLong(x.Agreements, 2000) {
		fields["agreements"] = "Los acuerdos admiten hasta 2000 caracteres."
	}
	if x.HeldOn.IsZero() || calendarDay(x.HeldOn).After(s.today()) {
		fields["heldOn"] = "La fecha no puede ser futura."
	}
	if len(fields) > 0 {
		return domain.Interview{}, domain.Validation(fields)
	}
	x.AuthorID, x.AuthorName = sc.userID, sc.name()
	saved, err := s.repo.SaveInterview(ctx, sc.institution.ID, x)
	if err == nil {
		s.changed(ctx, sc, "tutoring", saved.StudentID)
	}
	return saved, err
}

func (s *WellbeingService) DeleteInterview(ctx context.Context, token, slug, id string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	x, err := s.repo.Interview(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	st, err := s.student(ctx, sc, x.StudentID)
	if err != nil {
		return err
	}
	// Only the author (or dirección) removes an interview
	if !sc.office() && (!sc.tutors(st) || x.AuthorID != sc.userID) {
		return domain.ErrWellbeingReadOnly
	}
	if err := s.repo.DeleteInterview(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "tutoring", x.StudentID)
	return nil
}

func (s *WellbeingService) SaveCommitment(ctx context.Context, token, slug string, c domain.Commitment) (domain.Commitment, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.Commitment{}, err
	}
	if c.ID != "" {
		old, err := s.repo.Commitment(ctx, sc.institution.ID, c.ID)
		if err != nil {
			return domain.Commitment{}, err
		}
		c.StudentID = old.StudentID
	}
	st, err := s.student(ctx, sc, c.StudentID)
	if err != nil {
		return domain.Commitment{}, err
	}
	if !sc.tutors(st) {
		return domain.Commitment{}, domain.ErrNotYourTutee
	}
	c.Description = strings.TrimSpace(c.Description)
	fields := map[string]string{}
	if c.Party != "student" && c.Party != "family" && c.Party != "school" {
		fields["party"] = "Elige quién se compromete."
	}
	if c.Description == "" || tooLong(c.Description, 500) {
		fields["description"] = "Describe el compromiso (hasta 500 caracteres)."
	}
	if c.DueOn.IsZero() {
		fields["dueOn"] = "Indica cuándo se revisa."
	}
	if c.InterviewID != "" {
		if iv, err := s.repo.Interview(ctx, sc.institution.ID, c.InterviewID); err != nil || iv.StudentID != st.ID {
			fields["interviewId"] = "La entrevista no es de este estudiante."
		}
	}
	if len(fields) > 0 {
		return domain.Commitment{}, domain.Validation(fields)
	}
	c.AuthorName = sc.name()
	saved, err := s.repo.SaveCommitment(ctx, sc.institution.ID, c)
	if err == nil {
		s.changed(ctx, sc, "tutoring", saved.StudentID)
	}
	return saved, err
}

// CheckCommitment marks a commitment met, unmet or back to pending
func (s *WellbeingService) CheckCommitment(ctx context.Context, token, slug, id, status string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	c, err := s.repo.Commitment(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	st, err := s.student(ctx, sc, c.StudentID)
	if err != nil {
		return err
	}
	if !sc.tutors(st) {
		return domain.ErrNotYourTutee
	}
	var on *time.Time
	switch status {
	case "met", "unmet":
		t := s.today()
		on = &t
	case "pending":
	default:
		return domain.Validation(map[string]string{"status": "Estado no válido."})
	}
	if err := s.repo.CheckCommitment(ctx, sc.institution.ID, id, status, on); err != nil {
		return err
	}
	s.changed(ctx, sc, "tutoring", c.StudentID)
	return nil
}

func (s *WellbeingService) DeleteCommitment(ctx context.Context, token, slug, id string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	c, err := s.repo.Commitment(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	st, err := s.student(ctx, sc, c.StudentID)
	if err != nil {
		return err
	}
	if !sc.tutors(st) {
		return domain.ErrNotYourTutee
	}
	if err := s.repo.DeleteCommitment(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "tutoring", c.StudentID)
	return nil
}

// TutoringBoard is the tutor's (or dirección's) view: the students with their open matters, the commitments that
// are due and the latest interviews
type TutoringBoard struct {
	Students   []domain.WellStudent
	Due        []domain.Commitment
	Interviews []domain.Interview
	CanTutor   bool
	Family     bool
	Today      time.Time
}

func (s *WellbeingService) Tutoring(ctx context.Context, token, slug string) (TutoringBoard, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return TutoringBoard{}, err
	}
	b := TutoringBoard{Today: s.today(), Family: sc.family(), CanTutor: sc.office() || len(sc.tutoring) > 0}
	f := port.WellStudentFilter{InstitutionID: sc.institution.ID}
	switch {
	case sc.office() || sc.psychologist():
	case sc.family():
		f.StudentIDs = sc.children
	default:
		// Tutoring is for the tutor of the section: teachers without a section see nothing here
		f.SectionIDs = append([]string{}, sc.tutoring...)
	}
	if b.Students, err = s.repo.Students(ctx, f); err != nil {
		return b, err
	}
	var ids []string
	if f.StudentIDs != nil || f.SectionIDs != nil {
		ids = []string{}
		for _, st := range b.Students {
			ids = append(ids, st.ID)
		}
	}
	if b.Due, err = s.repo.Commitments(ctx, sc.institution.ID, ids, true); err != nil {
		return b, err
	}
	if sc.family() {
		return b, nil
	}
	from := b.Today.AddDate(0, 0, -60)
	b.Interviews, err = s.repo.Interviews(ctx, sc.institution.ID, ids, &from)
	if len(b.Interviews) > 30 {
		b.Interviews = b.Interviews[:30]
	}
	return b, err
}

// ---------- psychology ----------

var (
	caseOrigins  = map[string]bool{"tutor": true, "teacher": true, "family": true, "direction": true, "self": true, "psychologist": true}
	caseStatuses = map[string]bool{"referred": true, "open": true, "follow_up": true, "closed": true}
	sessionKinds = map[string]bool{"individual": true, "family": true, "evaluation": true, "coordination": true}
)

var ErrCaseOpen = domain.Validation(map[string]string{"studentId": "El estudiante ya tiene un caso abierto en psicología."})

// Refer opens a psychology case from a referral. The origin follows the role: the tutor of the section, a teacher,
// the family, dirección or the psychologist themself
func (s *WellbeingService) Refer(ctx context.Context, token, slug, studentID, reason, priority string) (domain.PsychCase, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.PsychCase{}, err
	}
	st, err := s.student(ctx, sc, studentID)
	if err != nil {
		return domain.PsychCase{}, err
	}
	if sc.specialist() {
		return domain.PsychCase{}, domain.ErrWellbeingReadOnly
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || tooLong(reason, 2000) {
		return domain.PsychCase{}, domain.Validation(map[string]string{"reason": "Cuenta el motivo de la derivación (hasta 2000 caracteres)."})
	}
	if priority != "urgent" {
		priority = "normal"
	}
	open, err := s.repo.Cases(ctx, sc.institution.ID, []string{st.ID}, "active", "")
	if err != nil {
		return domain.PsychCase{}, err
	}
	if len(open) > 0 {
		return domain.PsychCase{}, ErrCaseOpen
	}
	origin := "teacher"
	switch {
	case sc.psychologist():
		origin = "psychologist"
	case sc.family():
		origin = "family"
	case sc.office():
		origin = "direction"
	case slices.Contains(sc.tutoring, st.SectionID):
		origin = "tutor"
	}
	c := domain.PsychCase{StudentID: st.ID, Origin: origin, ReferredBy: sc.userID, ReferredName: sc.name(), Reason: reason, Priority: priority, Status: "referred", OpenedOn: s.today()}
	if sc.psychologist() {
		c.Status, c.PsychologistName = "open", sc.name()
	}
	saved, err := s.repo.OpenCase(ctx, sc.institution.ID, c)
	if err != nil {
		return domain.PsychCase{}, err
	}
	s.changed(ctx, sc, "psychology", "")
	if !sc.psychologist() {
		saved = referrerView(saved)
	}
	return saved, nil
}

// PsychBoard: psychologists get the cases; dirección and coordinación only the counts (the cases are confidential);
// everyone else their own referrals
type PsychBoard struct {
	Cases        []domain.PsychCase
	ByStatus     map[string]int
	Urgent       int
	Confidential bool
	CanManage    bool
}

func (s *WellbeingService) Psychology(ctx context.Context, token, slug, status string) (PsychBoard, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return PsychBoard{}, err
	}
	if sc.family() || sc.specialist() {
		return PsychBoard{}, domain.ErrPsychConfidential
	}
	all, err := s.repo.Cases(ctx, sc.institution.ID, nil, "", "")
	if err != nil {
		return PsychBoard{}, err
	}
	b := PsychBoard{ByStatus: map[string]int{}, CanManage: sc.psychologist(), Confidential: !sc.psychologist()}
	for _, c := range all {
		b.ByStatus[c.Status]++
		if c.Priority == "urgent" && c.Status != "closed" {
			b.Urgent++
		}
	}
	for _, c := range all {
		switch {
		case sc.psychologist():
			if status == "" || c.Status == status || (status == "active" && c.Status != "closed") {
				b.Cases = append(b.Cases, c)
			}
		case c.ReferredBy == sc.userID:
			b.Cases = append(b.Cases, referrerView(c))
		}
	}
	return b, nil
}

type CaseDetail struct {
	Case     domain.PsychCase
	Sessions []domain.PsychSession
}

func (s *WellbeingService) psychCase(ctx context.Context, sc wellScope, id string) (domain.PsychCase, error) {
	if !sc.psychologist() {
		return domain.PsychCase{}, domain.ErrPsychConfidential
	}
	return s.repo.Case(ctx, sc.institution.ID, id)
}

func (s *WellbeingService) Case(ctx context.Context, token, slug, id string) (CaseDetail, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return CaseDetail{}, err
	}
	c, err := s.psychCase(ctx, sc, id)
	if err != nil {
		return CaseDetail{}, err
	}
	sessions, err := s.repo.Sessions(ctx, c.ID)
	return CaseDetail{Case: c, Sessions: sessions}, err
}

// UpdateCase changes status, priority, the reason and the note shared with whoever referred
func (s *WellbeingService) UpdateCase(ctx context.Context, token, slug string, in domain.PsychCase) (domain.PsychCase, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.PsychCase{}, err
	}
	c, err := s.psychCase(ctx, sc, in.ID)
	if err != nil {
		return domain.PsychCase{}, err
	}
	fields := map[string]string{}
	if !caseStatuses[in.Status] {
		fields["status"] = "Estado no válido."
	}
	if in.Priority != "normal" && in.Priority != "urgent" {
		fields["priority"] = "Prioridad no válida."
	}
	in.SharedNote, in.Reason = strings.TrimSpace(in.SharedNote), strings.TrimSpace(in.Reason)
	if tooLong(in.SharedNote, 1000) {
		fields["sharedNote"] = "La nota compartida admite hasta 1000 caracteres."
	}
	if in.Reason == "" || tooLong(in.Reason, 2000) {
		fields["reason"] = "El motivo es obligatorio (hasta 2000 caracteres)."
	}
	if len(fields) > 0 {
		return domain.PsychCase{}, domain.Validation(fields)
	}
	c.Status, c.Priority, c.SharedNote, c.Reason = in.Status, in.Priority, in.SharedNote, in.Reason
	c.ClosedOn = nil
	if c.Status == "closed" {
		t := s.today()
		c.ClosedOn = &t
	}
	if c.PsychologistName == "" {
		c.PsychologistName = sc.name()
	}
	if err := s.repo.UpdateCase(ctx, sc.institution.ID, c); err != nil {
		return domain.PsychCase{}, err
	}
	s.changed(ctx, sc, "psychology", "")
	return s.repo.Case(ctx, sc.institution.ID, c.ID)
}

// AddSession logs a session; the first one takes a referred case to open
func (s *WellbeingService) AddSession(ctx context.Context, token, slug string, in domain.PsychSession) (domain.PsychSession, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.PsychSession{}, err
	}
	c, err := s.psychCase(ctx, sc, in.CaseID)
	if err != nil {
		return domain.PsychSession{}, err
	}
	in.Notes, in.NextSteps = strings.TrimSpace(in.Notes), strings.TrimSpace(in.NextSteps)
	fields := map[string]string{}
	if !sessionKinds[in.Kind] {
		fields["kind"] = "Elige el tipo de sesión."
	}
	if in.HeldOn.IsZero() || calendarDay(in.HeldOn).After(s.today()) {
		fields["heldOn"] = "La fecha no puede ser futura."
	}
	if in.Notes == "" || tooLong(in.Notes, 4000) {
		fields["notes"] = "Escribe las notas de la sesión (hasta 4000 caracteres)."
	}
	if tooLong(in.NextSteps, 1000) {
		fields["nextSteps"] = "Los siguientes pasos admiten hasta 1000 caracteres."
	}
	if len(fields) > 0 {
		return domain.PsychSession{}, domain.Validation(fields)
	}
	in.AuthorName = sc.name()
	saved, err := s.repo.AddSession(ctx, in)
	if err != nil {
		return domain.PsychSession{}, err
	}
	if c.Status == "referred" || c.Status == "closed" {
		c.Status, c.ClosedOn = "open", nil
		if c.PsychologistName == "" {
			c.PsychologistName = sc.name()
		}
		if err := s.repo.UpdateCase(ctx, sc.institution.ID, c); err != nil {
			return domain.PsychSession{}, err
		}
	}
	s.changed(ctx, sc, "psychology", "")
	return saved, nil
}

func (s *WellbeingService) DeleteSession(ctx context.Context, token, slug, caseID, id string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	if _, err := s.psychCase(ctx, sc, caseID); err != nil {
		return err
	}
	if err := s.repo.DeleteSession(ctx, caseID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "psychology", "")
	return nil
}

// ---------- alerts ----------

type WellAlerts struct {
	Alerts []domain.WellAlert
	Counts map[string]int
	Today  time.Time
}

// Alerts gathers what needs attention for the person: absences near the limit, failing conduct, serious faults
// still open, reconsiderations waiting for dirección, commitments past due, psychology referrals not yet seen and
// special-education students without a POI
func (s *WellbeingService) Alerts(ctx context.Context, token, slug string) (WellAlerts, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return WellAlerts{}, err
	}
	if sc.family() {
		return WellAlerts{}, domain.ErrWellbeingForbidden
	}
	today := s.today()
	out := WellAlerts{Counts: map[string]int{}, Today: today}
	add := func(a domain.WellAlert) {
		out.Alerts = append(out.Alerts, a)
		out.Counts[a.Kind]++
	}
	inst := sc.institution.ID
	f := sc.filter()
	students, err := s.repo.Students(ctx, f)
	if err != nil {
		return out, err
	}
	mine := map[string]domain.WellStudent{}
	var ids []string
	if !sc.seesAll() {
		ids = []string{}
	}
	for _, st := range students {
		mine[st.ID] = st
		if ids != nil {
			ids = append(ids, st.ID)
		}
	}
	periodID, _, err := s.repo.CurrentPeriod(ctx, inst, today)
	if err != nil {
		return out, err
	}

	// Absences: the share of days missed against the institution's limit
	if periodID != "" && !sc.specialist() {
		settings, err := s.attendance.settings(ctx, sc.attendanceScope)
		if err != nil {
			return out, err
		}
		tallies, err := s.attendance.repo.StudentTallies(ctx, inst, periodID, "")
		if err != nil {
			return out, err
		}
		limit := float64(settings.AbsenceLimitPercent)
		for _, t := range tallies {
			st, ok := mine[t.StudentID]
			if !ok || t.Days.Total() < 10 || t.Days.AbsentPercent() < warnAt(settings.AbsenceLimitPercent) {
				continue
			}
			level := "medium"
			if t.Days.AbsentPercent() >= limit {
				level = "high"
			}
			add(domain.WellAlert{Kind: "attendance", Level: level, StudentID: st.ID, StudentName: st.Name, Placement: st.Placement,
				Title: "Inasistencias cerca del límite", Detail: percentText(t.Days.AbsentPercent()) + " de faltas (límite " + percentText(limit) + ")"})
		}
	}

	// Distance form (EBA): no activity, work past its deadline, permanence under the institution's rule
	if s.participation != nil && !sc.specialist() {
		dropouts, err := s.participation.DropoutAlerts(ctx, inst)
		if err != nil {
			return out, err
		}
		for _, d := range dropouts {
			st, ok := mine[d.Signal.StudentID]
			if !ok {
				continue
			}
			add(domain.WellAlert{Kind: "dropout", Level: d.Level, StudentID: st.ID, StudentName: st.Name, Placement: d.Signal.Placement,
				Title: d.Title, Detail: d.Detail, Since: d.Signal.LastAt})
		}
	}

	// Conduct below 11 in the latest term graded
	if periodID != "" && !sc.specialist() {
		var sections []string
		if !sc.seesAll() {
			sections = union(sc.tutoring, sc.teaching)
		}
		low, err := s.repo.LowConduct(ctx, inst, periodID, sections)
		if err != nil {
			return out, err
		}
		for _, l := range low {
			add(domain.WellAlert{Kind: "conduct", Level: "high", StudentID: l.StudentID, StudentName: l.StudentName, Placement: l.Placement,
				Title: "Nota de conducta desaprobatoria", Detail: itoa(l.Score) + " en " + l.TermName})
		}
	}

	// Discipline: serious faults still open, and reconsiderations waiting (dirección has five school days)
	if !sc.specialist() {
		incidents, err := s.repo.Incidents(ctx, domain.IncidentFilter{InstitutionID: inst, StudentIDs: ids, Status: "open"})
		if err != nil {
			return out, err
		}
		for _, x := range incidents {
			if x.Severity != "minor" {
				level := "medium"
				if x.Severity == "very_serious" {
					level = "high"
				}
				on := x.OccurredOn
				add(domain.WellAlert{Kind: "discipline", Level: level, StudentID: x.StudentID, StudentName: x.StudentName, Placement: x.Placement,
					Title: severityLabel(x.Severity) + " sin cerrar", Detail: categoryLabel(x.Category), Since: &on})
			}
		}
		rows, err := s.repo.MeasureRows(ctx, inst, ids, time.Date(today.Year(), 1, 1, 0, 0, 0, 0, time.UTC))
		if err != nil {
			return out, err
		}
		for _, m := range rows {
			if m.ReconsiderationOn != nil && m.ResolvedOn == nil {
				due := addSchoolDays(*m.ReconsiderationOn, 5)
				add(domain.WellAlert{Kind: "reconsideration", Level: "high", StudentID: m.StudentID, StudentName: m.StudentName, Placement: m.Placement,
					Title: "Reconsideración por resolver: " + measureLabel(m.Kind), Detail: "Plazo hasta el " + due.Format("02/01/2006"), Since: m.ReconsiderationOn})
			}
		}
	}

	// Commitments past their date (the tutor's own sections, or all for dirección)
	if !sc.specialist() && (sc.office() || sc.psychologist() || len(sc.tutoring) > 0) {
		tutees := ids
		if !sc.seesAll() {
			tutees = []string{}
			for _, st := range students {
				if slices.Contains(sc.tutoring, st.SectionID) {
					tutees = append(tutees, st.ID)
				}
			}
		}
		due, err := s.repo.Commitments(ctx, inst, tutees, true)
		if err != nil {
			return out, err
		}
		for _, c := range due {
			if c.DueOn.Before(today) {
				on := c.DueOn
				add(domain.WellAlert{Kind: "commitment", Level: "medium", StudentID: c.StudentID, StudentName: c.StudentName, Placement: c.Placement,
					Title: "Compromiso vencido sin revisar", Detail: c.Description, Since: &on})
			}
		}
	}

	// Psychology: referrals nobody has seen yet (only the psychologists see who)
	if sc.psychologist() {
		cases, err := s.repo.Cases(ctx, inst, nil, "referred", "")
		if err != nil {
			return out, err
		}
		for _, c := range cases {
			level := "medium"
			if c.Priority == "urgent" {
				level = "high"
			}
			on := c.OpenedOn
			add(domain.WellAlert{Kind: "psychology", Level: level, StudentID: c.StudentID, StudentName: c.StudentName, Placement: c.Placement,
				Title: "Derivación pendiente de atender", Detail: "Derivado por " + c.ReferredName, Since: &on})
		}
	} else if sc.office() {
		cases, err := s.repo.Cases(ctx, inst, nil, "referred", "")
		if err != nil {
			return out, err
		}
		if n := len(cases); n > 0 {
			add(domain.WellAlert{Kind: "psychology", Level: "medium", Title: "Derivaciones a psicología sin atender", Detail: itoa(n) + " pendientes (el detalle es confidencial)"})
		}
	}

	// Special education: students with special needs and no POI this period
	if sc.ebe() && periodID != "" && (sc.office() || sc.specialist()) {
		for _, st := range students {
			if st.SpecialNeeds && !st.HasPlan {
				add(domain.WellAlert{Kind: "plan", Level: "medium", StudentID: st.ID, StudentName: st.Name, Placement: st.Placement,
					Title: "Sin plan de orientación individual", Detail: "Aún no tiene POI en el periodo"})
			}
		}
	}

	// High first, then by student
	slices.SortStableFunc(out.Alerts, func(a, b domain.WellAlert) int {
		if a.Level != b.Level {
			if a.Level == "high" {
				return -1
			}
			return 1
		}
		return strings.Compare(a.StudentName, b.StudentName)
	})
	return out, nil
}
