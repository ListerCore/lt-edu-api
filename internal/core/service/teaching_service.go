package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"api/internal/core/domain"
	"api/internal/core/port"
)

var (
	// Coordinación builds the load and the timetable; the rest of the staff reads it
	teachingManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true}
	teachingViewers  = map[string]bool{"program_coordinator": true, "special_ed_specialist": true, "secretary": true, "teacher": true, "tutor": true, "assistant": true, "psychologist": true, "treasurer": true}
	clockPattern     = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
)

// TeachingService runs the teaching load (who teaches what in each section), the tutor of each section, the school
// day of each shift and the weekly timetable (phase 4 of docs/plan-academico.md)
type TeachingService struct {
	repo      port.TeachingRepository
	staff     port.StaffRepository
	curricula port.CurriculumRepository
	academic  *AcademicService
}

func NewTeachingService(repo port.TeachingRepository, staff port.StaffRepository, curricula port.CurriculumRepository, academic *AcademicService) *TeachingService {
	return &TeachingService{repo: repo, staff: staff, curricula: curricula, academic: academic}
}

func (s *TeachingService) scope(ctx context.Context, token, slug string, write bool) (academicScope, error) {
	sc, err := resolveInstitution(ctx, s.academic.auth, s.academic.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return academicScope{}, err
	}
	switch {
	case teachingManagers[sc.role]:
	case teachingViewers[sc.role]:
		if write {
			return academicScope{}, domain.ErrAcademicReadOnly
		}
	default:
		return academicScope{}, domain.ErrScheduleForbidden
	}
	return academicScope{institutionScope: sc, profile: profileFor(sc.institution.Kind)}, nil
}

func (s *TeachingService) changed(ctx context.Context, sc academicScope, area string) {
	s.academic.changed(ctx, sc, area)
}

// Planner is everything the load and timetable pages work with for one period
type Planner struct {
	Context     AcademicContext
	Period      domain.Period
	Sections    []domain.Section
	Blocks      []domain.TimeBlock
	Assignments []domain.Assignment
	Entries     []domain.ScheduleEntry
	Teachers    []domain.Staff
	// Classes of the same teachers in other periods that run at the same time (CEPRE cycles), to show them busy
	Elsewhere []domain.ScheduleEntry
}

func (s *TeachingService) Planner(ctx context.Context, token, slug, periodID string) (Planner, error) {
	sc, err := s.scope(ctx, token, slug, false)
	if err != nil {
		return Planner{}, err
	}
	actx, err := s.academic.contextFor(ctx, sc)
	if err != nil {
		return Planner{}, err
	}
	p, err := s.academic.repo.Period(ctx, sc.institution.ID, periodID)
	if err != nil {
		return Planner{}, err
	}
	sections, err := s.academic.repo.Sections(ctx, sc.institution.ID, periodID)
	if err != nil {
		return Planner{}, err
	}
	blocks, err := s.repo.TimeBlocks(ctx, sc.institution.ID)
	if err != nil {
		return Planner{}, err
	}
	assignments, err := s.repo.Assignments(ctx, sc.institution.ID, periodID)
	if err != nil {
		return Planner{}, err
	}
	entries, err := s.repo.Entries(ctx, sc.institution.ID, []string{periodID})
	if err != nil {
		return Planner{}, err
	}
	teachers, _, err := s.staff.StaffList(ctx, domain.StaffFilter{InstitutionID: sc.institution.ID, Status: "active", TeachersOnly: true, Limit: 300})
	if err != nil {
		return Planner{}, err
	}
	others, err := s.overlapping(ctx, sc, p, false)
	if err != nil {
		return Planner{}, err
	}
	var elsewhere []domain.ScheduleEntry
	if len(others) > 0 {
		if elsewhere, err = s.repo.Entries(ctx, sc.institution.ID, others); err != nil {
			return Planner{}, err
		}
	}
	return Planner{Context: actx, Period: p, Sections: sections, Blocks: blocks, Assignments: assignments, Entries: entries, Teachers: teachers, Elsewhere: elsewhere}, nil
}

// overlapping lists the ids of the open periods whose dates cross p's (with p itself when withSelf)
func (s *TeachingService) overlapping(ctx context.Context, sc academicScope, p domain.Period, withSelf bool) ([]string, error) {
	periods, err := s.academic.repo.Periods(ctx, sc.institution.ID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, o := range periods {
		if o.ID == p.ID {
			if withSelf {
				ids = append(ids, o.ID)
			}
			continue
		}
		if o.Status != domain.PeriodClosed && !o.StartsOn.After(p.EndsOn) && !o.EndsOn.Before(p.StartsOn) {
			ids = append(ids, o.ID)
		}
	}
	return ids, nil
}

func (s *TeachingService) openPeriod(ctx context.Context, sc academicScope, periodID string) (domain.Period, error) {
	p, err := s.academic.repo.Period(ctx, sc.institution.ID, periodID)
	if err != nil {
		return domain.Period{}, err
	}
	if p.Status == domain.PeriodClosed {
		return domain.Period{}, domain.ErrPeriodClosed
	}
	return p, nil
}

// ---------- teaching load ----------

// PrepareLoad creates, for every section of the period, one assignment per subject of the plan in force for its
// grade (EFSRT happen in workplaces and are left out). Existing ones are kept. Returns how many were added
func (s *TeachingService) PrepareLoad(ctx context.Context, token, slug, periodID string) (int, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return 0, err
	}
	if _, err := s.openPeriod(ctx, sc, periodID); err != nil {
		return 0, err
	}
	items, err := s.loadFromPlans(ctx, sc, periodID)
	if err != nil {
		return 0, err
	}
	added, err := s.repo.CreateAssignments(ctx, sc.institution.ID, items)
	if err == nil && added > 0 {
		s.changed(ctx, sc, "teaching")
	}
	return added, err
}

func (s *TeachingService) loadFromPlans(ctx context.Context, sc academicScope, periodID string) ([]domain.Assignment, error) {
	sections, err := s.academic.repo.Sections(ctx, sc.institution.ID, periodID)
	if err != nil {
		return nil, err
	}
	plans, err := s.curricula.Curricula(ctx, sc.institution.ID)
	if err != nil {
		return nil, err
	}
	byLevel := map[string]domain.Curriculum{}
	for _, p := range plans {
		if p.Status != domain.CurriculumActive {
			continue
		}
		full, err := s.curricula.Curriculum(ctx, sc.institution.ID, p.ID)
		if err != nil {
			return nil, err
		}
		byLevel[p.LevelID] = full
	}
	var items []domain.Assignment
	for _, sec := range sections {
		plan, ok := byLevel[sec.LevelID]
		if !ok {
			continue
		}
		for _, sub := range plan.Subjects {
			if sub.Kind == domain.SubjectEFSRT || (sc.profile.hasGrades && !slices.Contains(sub.GradeIDs, sec.GradeID)) {
				continue
			}
			hours := 0
			if sub.WeeklyHours != nil {
				hours = *sub.WeeklyHours
			}
			items = append(items, domain.Assignment{SectionID: sec.ID, SubjectID: sub.ID, WeeklyHours: hours})
		}
	}
	return items, nil
}

// teacherFree checks the teacher has no class at that day and time in the period or one running at the same time
func (s *TeachingService) teacherFree(ctx context.Context, sc academicScope, p domain.Period, staffID string, weekday int, start, end string, skip func(domain.ScheduleEntry) bool) error {
	periods, err := s.overlapping(ctx, sc, p, true)
	if err != nil {
		return err
	}
	busy, err := s.repo.StaffEntries(ctx, sc.institution.ID, staffID, periods)
	if err != nil {
		return err
	}
	for _, e := range busy {
		if e.Weekday == weekday && e.StartsAt < end && start < e.EndsAt && !skip(e) {
			return &domain.Error{Kind: domain.KindConflict, Code: domain.ErrTeacherBusy.Code, Message: fmt.Sprintf("%s ya tiene %s en %s a esa hora.", e.StaffName, e.SubjectName, e.SectionLabel)}
		}
	}
	return nil
}

// UpdateAssignment sets the teacher (empty: unassigned) and the weekly hours of a subject in a section. A new teacher
// must be free at every hour the subject already has in the timetable
func (s *TeachingService) UpdateAssignment(ctx context.Context, token, slug, id, staffID string, weeklyHours int) (domain.Assignment, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Assignment{}, err
	}
	a, err := s.repo.Assignment(ctx, sc.institution.ID, id)
	if err != nil {
		return domain.Assignment{}, err
	}
	p, err := s.openPeriod(ctx, sc, a.PeriodID)
	if err != nil {
		return domain.Assignment{}, err
	}
	if weeklyHours < 0 || weeklyHours > 40 {
		return domain.Assignment{}, domain.Validation(map[string]string{"weeklyHours": "Entre 0 y 40 horas."})
	}
	if weeklyHours < a.Scheduled {
		return domain.Assignment{}, domain.Validation(map[string]string{"weeklyHours": fmt.Sprintf("Ya tiene %d horas en el horario: quítalas antes de bajar sus horas.", a.Scheduled)})
	}
	if staffID != "" && staffID != a.StaffID {
		st, err := s.staff.Staff(ctx, sc.institution.ID, staffID)
		if err != nil || !st.IsTeacher || st.Status != "active" {
			return domain.Assignment{}, domain.ErrNotATeacher
		}
		entries, err := s.repo.SectionEntries(ctx, sc.institution.ID, a.SectionID)
		if err != nil {
			return domain.Assignment{}, err
		}
		for _, e := range entries {
			if e.AssignmentID != a.ID {
				continue
			}
			if err := s.teacherFree(ctx, sc, p, staffID, e.Weekday, e.StartsAt, e.EndsAt, func(x domain.ScheduleEntry) bool { return x.AssignmentID == a.ID }); err != nil {
				return domain.Assignment{}, err
			}
		}
	}
	out, err := s.repo.UpdateAssignment(ctx, sc.institution.ID, id, staffID, weeklyHours)
	if err == nil {
		s.changed(ctx, sc, "teaching")
	}
	return out, err
}

func (s *TeachingService) DeleteAssignment(ctx context.Context, token, slug, id string) error {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	a, err := s.repo.Assignment(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if _, err := s.openPeriod(ctx, sc, a.PeriodID); err != nil {
		return err
	}
	if err := s.repo.DeleteAssignment(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "teaching")
	return nil
}

func (s *TeachingService) SetTutor(ctx context.Context, token, slug, sectionID, staffID string) error {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	if staffID != "" {
		st, err := s.staff.Staff(ctx, sc.institution.ID, staffID)
		if err != nil || st.Status != "active" {
			return domain.Validation(map[string]string{"staffId": "Elige a alguien del personal activo."})
		}
	}
	if err := s.repo.SetTutor(ctx, sc.institution.ID, sectionID, staffID); err != nil {
		return err
	}
	s.changed(ctx, sc, "teaching")
	return nil
}

// ---------- school day ----------

// SetTimeBlocks replaces the day of a shift. Blocks must be in order and not overlap
func (s *TeachingService) SetTimeBlocks(ctx context.Context, token, slug, shift string, blocks []domain.TimeBlock) ([]domain.TimeBlock, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return nil, err
	}
	if !validShifts[shift] {
		return nil, domain.Validation(map[string]string{"shift": "Elige un turno."})
	}
	if len(blocks) > 16 {
		return nil, domain.Validation(map[string]string{"blocks": "Como máximo 16 bloques por turno."})
	}
	prevEnd := ""
	for i := range blocks {
		b := &blocks[i]
		b.Position = i
		if b.Kind == "" {
			b.Kind = "class"
		}
		switch {
		case !clockPattern.MatchString(b.StartsAt) || !clockPattern.MatchString(b.EndsAt):
			return nil, domain.Validation(map[string]string{"blocks": fmt.Sprintf("Bloque %d: escribe la hora como 07:45.", i+1)})
		case b.EndsAt <= b.StartsAt:
			return nil, domain.Validation(map[string]string{"blocks": fmt.Sprintf("Bloque %d: debe terminar después de empezar.", i+1)})
		case prevEnd != "" && b.StartsAt < prevEnd:
			return nil, domain.Validation(map[string]string{"blocks": fmt.Sprintf("Bloque %d: se cruza con el anterior.", i+1)})
		case b.Kind != "class" && b.Kind != "break":
			return nil, domain.Validation(map[string]string{"blocks": "Tipo de bloque no válido."})
		}
		prevEnd = b.EndsAt
	}
	if err := s.repo.ReplaceTimeBlocks(ctx, sc.institution.ID, shift, blocks); err != nil {
		return nil, err
	}
	s.changed(ctx, sc, "teaching")
	return s.repo.TimeBlocks(ctx, sc.institution.ID)
}

// ---------- timetable ----------

func (s *TeachingService) section(ctx context.Context, sc academicScope, sectionID string) (domain.Section, error) {
	sec, err := s.academic.repo.Section(ctx, sc.institution.ID, sectionID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Section{}, domain.Validation(map[string]string{"sectionId": "Elige una sección."})
	}
	return sec, err
}

// PlaceClass puts an assignment of the section on a day and block. Refused when the block is not a class hour of the
// section's shift, the subject already has all its hours, or the teacher or the room are busy at that time
func (s *TeachingService) PlaceClass(ctx context.Context, token, slug string, slot domain.Slot, room string) (domain.ScheduleEntry, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.ScheduleEntry{}, err
	}
	sec, err := s.section(ctx, sc, slot.SectionID)
	if err != nil {
		return domain.ScheduleEntry{}, err
	}
	p, err := s.openPeriod(ctx, sc, sec.PeriodID)
	if err != nil {
		return domain.ScheduleEntry{}, err
	}
	settings, err := s.academic.settings(ctx, sc)
	if err != nil {
		return domain.ScheduleEntry{}, err
	}
	if !slices.Contains(settings.WeekDays, slot.Weekday) {
		return domain.ScheduleEntry{}, domain.Validation(map[string]string{"weekday": "La institución no tiene clases ese día."})
	}
	blocks, err := s.repo.TimeBlocks(ctx, sc.institution.ID)
	if err != nil {
		return domain.ScheduleEntry{}, err
	}
	bi := slices.IndexFunc(blocks, func(b domain.TimeBlock) bool { return b.ID == slot.BlockID })
	if bi < 0 || blocks[bi].Shift != sec.Shift || blocks[bi].Kind != "class" {
		return domain.ScheduleEntry{}, domain.Validation(map[string]string{"blockId": "Elige una hora de clase del turno de la sección."})
	}
	block := blocks[bi]
	a, err := s.repo.Assignment(ctx, sc.institution.ID, slot.AssignmentID)
	if err != nil || a.SectionID != sec.ID {
		return domain.ScheduleEntry{}, domain.Validation(map[string]string{"assignmentId": "Ese curso no es de esta sección."})
	}
	room = strings.TrimSpace(room)
	if utf8.RuneCountInString(room) > 60 {
		return domain.ScheduleEntry{}, domain.Validation(map[string]string{"room": "Máximo 60 caracteres."})
	}
	current, err := s.repo.SectionEntries(ctx, sc.institution.ID, sec.ID)
	if err != nil {
		return domain.ScheduleEntry{}, err
	}
	sameSlot := func(e domain.ScheduleEntry) bool {
		return e.SectionID == sec.ID && e.Weekday == slot.Weekday && e.BlockID == slot.BlockID
	}
	placed := 0
	for _, e := range current {
		if e.AssignmentID == a.ID && !sameSlot(e) {
			placed++
		}
	}
	if placed >= a.WeeklyHours {
		return domain.ScheduleEntry{}, domain.ErrHoursComplete
	}
	if a.StaffID != "" {
		if err := s.teacherFree(ctx, sc, p, a.StaffID, slot.Weekday, block.StartsAt, block.EndsAt, sameSlot); err != nil {
			return domain.ScheduleEntry{}, err
		}
	}
	if room != "" {
		periods, err := s.overlapping(ctx, sc, p, true)
		if err != nil {
			return domain.ScheduleEntry{}, err
		}
		all, err := s.repo.Entries(ctx, sc.institution.ID, periods)
		if err != nil {
			return domain.ScheduleEntry{}, err
		}
		for _, e := range all {
			if strings.EqualFold(e.Room, room) && e.Weekday == slot.Weekday && e.StartsAt < block.EndsAt && block.StartsAt < e.EndsAt && !sameSlot(e) {
				return domain.ScheduleEntry{}, &domain.Error{Kind: domain.KindConflict, Code: domain.ErrRoomBusy.Code, Message: fmt.Sprintf("%s ya está ocupado por %s (%s).", room, e.SectionLabel, e.SubjectName)}
			}
		}
	}
	out, err := s.repo.PlaceEntry(ctx, sc.institution.ID, sec.PeriodID, slot, room)
	if err == nil {
		s.changed(ctx, sc, "teaching")
	}
	return out, err
}

func (s *TeachingService) RemoveClass(ctx context.Context, token, slug, entryID string) error {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteEntry(ctx, sc.institution.ID, entryID); err != nil {
		return err
	}
	s.changed(ctx, sc, "teaching")
	return nil
}

func (s *TeachingService) ClearSection(ctx context.Context, token, slug, sectionID string) error {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	sec, err := s.section(ctx, sc, sectionID)
	if err != nil {
		return err
	}
	if _, err := s.openPeriod(ctx, sc, sec.PeriodID); err != nil {
		return err
	}
	if err := s.repo.ClearSection(ctx, sc.institution.ID, sectionID); err != nil {
		return err
	}
	s.changed(ctx, sc, "teaching")
	return nil
}

// AutoResult is what the automatic timetable did
type AutoResult struct {
	Placed int
	// Hours it could not place (no free hour where the teacher is free too)
	Pending int
}

// AutoFill completes the timetable of a section (or every section of the period when sectionID is empty) with the
// hours still missing, spreading each subject across the week and never putting a teacher in two places at once
func (s *TeachingService) AutoFill(ctx context.Context, token, slug, periodID, sectionID string) (AutoResult, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return AutoResult{}, err
	}
	res, err := s.autoFill(ctx, sc, periodID, sectionID)
	if err == nil && res.Placed > 0 {
		s.changed(ctx, sc, "teaching")
	}
	return res, err
}

type interval struct {
	weekday    int
	start, end string
}

func (s *TeachingService) autoFill(ctx context.Context, sc academicScope, periodID, sectionID string) (AutoResult, error) {
	p, err := s.openPeriod(ctx, sc, periodID)
	if err != nil {
		return AutoResult{}, err
	}
	settings, err := s.academic.settings(ctx, sc)
	if err != nil {
		return AutoResult{}, err
	}
	blocks, err := s.repo.TimeBlocks(ctx, sc.institution.ID)
	if err != nil {
		return AutoResult{}, err
	}
	sections, err := s.academic.repo.Sections(ctx, sc.institution.ID, periodID)
	if err != nil {
		return AutoResult{}, err
	}
	assignments, err := s.repo.Assignments(ctx, sc.institution.ID, periodID)
	if err != nil {
		return AutoResult{}, err
	}
	periods, err := s.overlapping(ctx, sc, p, true)
	if err != nil {
		return AutoResult{}, err
	}
	existing, err := s.repo.Entries(ctx, sc.institution.ID, periods)
	if err != nil {
		return AutoResult{}, err
	}
	busy := map[string][]interval{}
	taken := map[string]bool{} // section|weekday|block
	perDay := map[string]int{} // assignment|weekday
	for _, e := range existing {
		if e.StaffID != "" {
			busy[e.StaffID] = append(busy[e.StaffID], interval{e.Weekday, e.StartsAt, e.EndsAt})
		}
		taken[fmt.Sprintf("%s|%d|%s", e.SectionID, e.Weekday, e.BlockID)] = true
		perDay[fmt.Sprintf("%s|%d", e.AssignmentID, e.Weekday)]++
	}
	free := func(staffID string, day int, b domain.TimeBlock) bool {
		for _, iv := range busy[staffID] {
			if iv.weekday == day && iv.start < b.EndsAt && b.StartsAt < iv.end {
				return false
			}
		}
		return true
	}
	days := settings.WeekDays
	var slots []domain.Slot
	res := AutoResult{}
	for _, sec := range sections {
		if sectionID != "" && sec.ID != sectionID {
			continue
		}
		var classBlocks []domain.TimeBlock
		for _, b := range blocks {
			if b.Shift == sec.Shift && b.Kind == "class" {
				classBlocks = append(classBlocks, b)
			}
		}
		var own []domain.Assignment
		for _, a := range assignments {
			if a.SectionID == sec.ID && a.WeeklyHours > a.Scheduled {
				own = append(own, a)
			}
		}
		// The subjects with more hours go first: they are the hardest to spread
		sort.SliceStable(own, func(i, j int) bool { return own[i].WeeklyHours-own[i].Scheduled > own[j].WeeklyHours-own[j].Scheduled })
		for k, a := range own {
			remaining := a.WeeklyHours - a.Scheduled
			limit := (a.WeeklyHours + len(days) - 1) / len(days)
			for pass := 0; pass < 3 && remaining > 0; pass++ {
				for d := 0; d < len(days) && remaining > 0; d++ {
					// Each subject starts on a different day so the week stays balanced
					day := days[(d+k)%len(days)]
					key := fmt.Sprintf("%s|%d", a.ID, day)
					if perDay[key] >= limit+pass {
						continue
					}
					for bi := range classBlocks {
						// Rotate the starting hour too, so not every subject lands first thing
						b := classBlocks[(bi+k+d)%len(classBlocks)]
						slotKey := fmt.Sprintf("%s|%d|%s", sec.ID, day, b.ID)
						if taken[slotKey] || (a.StaffID != "" && !free(a.StaffID, day, b)) {
							continue
						}
						taken[slotKey] = true
						perDay[key]++
						if a.StaffID != "" {
							busy[a.StaffID] = append(busy[a.StaffID], interval{day, b.StartsAt, b.EndsAt})
						}
						slots = append(slots, domain.Slot{SectionID: sec.ID, AssignmentID: a.ID, Weekday: day, BlockID: b.ID})
						remaining--
						break
					}
				}
			}
			res.Pending += remaining
		}
	}
	if len(slots) > 0 {
		placed, err := s.repo.PlaceEntries(ctx, sc.institution.ID, periodID, slots)
		if err != nil {
			return res, err
		}
		res.Placed = placed
	}
	return res, nil
}

// ---------- my timetable ----------

// MySchedule is the week of the signed-in person: their own classes (teachers) or their section's (students and
// guardians, one per child)
type MySchedule struct {
	Kind     string // teacher | student | none
	WeekDays []int
	Blocks   []domain.TimeBlock
	Groups   []ScheduleGroup
}

type ScheduleGroup struct {
	Title   string
	Detail  string
	Entries []domain.ScheduleEntry
}

func (s *TeachingService) Mine(ctx context.Context, token, slug string) (MySchedule, error) {
	sc, err := resolveInstitution(ctx, s.academic.auth, s.academic.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return MySchedule{}, err
	}
	me, err := s.academic.auth.Principal(ctx, token)
	if err != nil {
		return MySchedule{}, err
	}
	asc := academicScope{institutionScope: sc, profile: profileFor(sc.institution.Kind)}
	settings, err := s.academic.settings(ctx, asc)
	if err != nil {
		return MySchedule{}, err
	}
	blocks, err := s.repo.TimeBlocks(ctx, sc.institution.ID)
	if err != nil {
		return MySchedule{}, err
	}
	out := MySchedule{Kind: "none", WeekDays: settings.WeekDays, Blocks: blocks}
	switch sc.role {
	case "student", "guardian":
		sections, err := s.repo.SectionsForEmail(ctx, sc.institution.ID, me.User.Email)
		if err != nil {
			return out, err
		}
		for _, x := range sections {
			entries, err := s.repo.SectionEntries(ctx, sc.institution.ID, x.SectionID)
			if err != nil {
				return out, err
			}
			out.Groups = append(out.Groups, ScheduleGroup{Title: x.StudentName, Detail: fmt.Sprintf("%s · %s · %s", x.SectionLabel, x.LevelName, x.PeriodName), Entries: entries})
			out.Kind = "student"
		}
	default:
		st, err := s.staff.StaffByEmail(ctx, sc.institution.ID, me.User.Email)
		if errors.Is(err, domain.ErrNotFound) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		periods, err := s.academic.repo.Periods(ctx, sc.institution.ID)
		if err != nil {
			return out, err
		}
		var running []string
		for _, p := range periods {
			if p.Status == domain.PeriodActive {
				running = append(running, p.ID)
			}
		}
		entries, err := s.repo.StaffEntries(ctx, sc.institution.ID, st.ID, running)
		if err != nil {
			return out, err
		}
		out.Kind = "teacher"
		out.Groups = []ScheduleGroup{{Title: st.GivenNames + " " + st.Surnames, Detail: fmt.Sprintf("%d horas a la semana", len(entries)), Entries: entries}}
	}
	return out, nil
}
