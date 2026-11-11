package service

import (
	"context"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

var (
	// Dirección, coordinación and secretaría read the whole board; tesorería only the collection
	reportsAcademic = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true, "secretary": true}
	reportsFinance  = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true, "secretary": true, "treasurer": true}
)

// Days the attendance chart covers when no range is asked for
const reportDefaultDays = 90

// Passing conduct mark (DS 021-2026-MINEDU)
const conductPassing = 11

// ReportsService builds the direction board (phase 12 of docs/plan-academico.md): enrollment, attendance,
// performance by competency or course, collection, and the comparison between terms and periods. It only reads
// aggregates, so nothing here names a student
type ReportsService struct {
	repo       port.ReportsRepository
	assessment *AssessmentService
}

func NewReportsService(repo port.ReportsRepository, assessment *AssessmentService) *ReportsService {
	return &ReportsService{repo: repo, assessment: assessment}
}

// ReportQuery is what the board is asked for; empty fields take the defaults (current period, every level,
// the last ninety days)
type ReportQuery struct {
	PeriodID string
	LevelID  string
	Subject  string
	From     *time.Time
	To       *time.Time
}

// ReportsBoard is the whole board. The academic parts are empty for treasurers, and each scale fills its own
// performance lists: literal (AD/A/B/C by term, area and group, plus conduct), vigesimal (unit averages) or score
// (mock exams and courses)
type ReportsBoard struct {
	Periods  []domain.Period
	Period   domain.Period
	Levels   []port.ReportOption
	LevelID  string
	Subject  string
	Scale    string
	Passing  int
	From     time.Time
	To       time.Time
	Today    time.Time
	Academic bool

	Enrollment         domain.EnrollmentStats
	AttendanceDays     []domain.AttendanceDay
	AttendanceGroups   []domain.GroupAttendance
	AttendanceTimeline []domain.GroupAttendance

	ResultTerms    []domain.ResultMix
	ResultSubjects []domain.ResultMix
	ResultGroups   []domain.ResultMix
	ConductTerms   []domain.ResultMix

	Finance      domain.FinanceTotals
	Months       []domain.MoneyFlow
	Methods      []domain.MethodTotal
	Aging        []domain.AgingBucket
	FinanceByGrp []domain.GroupDebt

	Compare []domain.PeriodKpi
}

func (s *ReportsService) scope(ctx context.Context, token, slug string) (attendanceScope, error) {
	isc, err := resolveInstitution(ctx, s.assessment.academic.auth, s.assessment.academic.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return attendanceScope{}, err
	}
	if !reportsFinance[isc.role] {
		return attendanceScope{}, domain.ErrReportsForbidden
	}
	return attendanceScope{academicScope: academicScope{institutionScope: isc, profile: profileFor(isc.institution.Kind)}}, nil
}

// reportRange clamps the asked range to the period and today; with nothing asked it is the last ninety days
func reportRange(p domain.Period, today time.Time, from, to *time.Time) (time.Time, time.Time) {
	end := p.EndsOn
	if today.Before(end) {
		end = today
	}
	if to != nil && to.Before(end) {
		end = *to
	}
	if end.Before(p.StartsOn) {
		end = p.StartsOn
	}
	start := end.AddDate(0, 0, -reportDefaultDays+1)
	if from != nil {
		start = *from
	}
	if start.Before(p.StartsOn) {
		start = p.StartsOn
	}
	if start.After(end) {
		start = end
	}
	return start, end
}

func (s *ReportsService) Board(ctx context.Context, token, slug string, q ReportQuery) (ReportsBoard, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return ReportsBoard{}, err
	}
	p, periods, err := s.assessment.period(ctx, gradeScope{attendanceScope: sc}, q.PeriodID)
	if err != nil {
		return ReportsBoard{}, err
	}
	today := s.assessment.attendance.today()
	out := ReportsBoard{Periods: periods, Period: p, Scale: sc.profile.scale, Today: today, Academic: reportsAcademic[sc.role], Subject: q.Subject}
	if sc.profile.passing != nil {
		out.Passing = *sc.profile.passing
	}
	if out.Levels, err = s.repo.Levels(ctx, sc.institution.ID); err != nil {
		return out, err
	}
	for _, l := range out.Levels {
		if l.ID == q.LevelID {
			out.LevelID = l.ID
		}
	}
	if q.LevelID != "" && out.LevelID == "" {
		return out, domain.ErrNotFound
	}
	if p.ID == "" {
		return out, nil
	}
	out.From, out.To = reportRange(p, today, q.From, q.To)
	f := domain.ReportFilter{InstitutionID: sc.institution.ID, PeriodID: p.ID, LevelID: out.LevelID, From: out.From, To: out.To, Today: today, Passing: out.Passing}

	if out.Enrollment, err = s.repo.Enrollment(ctx, f); err != nil {
		return out, err
	}
	if err := s.finance(ctx, f, &out); err != nil {
		return out, err
	}
	if out.Compare, err = s.repo.PeriodKpis(ctx, sc.institution.ID, out.Scale, out.Passing, today); err != nil {
		return out, err
	}
	if !out.Academic {
		// Treasurers compare only the collection
		for i := range out.Compare {
			out.Compare[i].Attendance, out.Compare[i].Achievement = -1, -1
		}
		return out, nil
	}
	if err := s.attendance(ctx, f, &out); err != nil {
		return out, err
	}
	return out, s.performance(ctx, f, sc.profile, &out)
}

func (s *ReportsService) attendance(ctx context.Context, f domain.ReportFilter, out *ReportsBoard) (err error) {
	if out.AttendanceDays, err = s.repo.AttendanceDays(ctx, f); err != nil {
		return err
	}
	if out.AttendanceGroups, err = s.repo.AttendanceGroups(ctx, f); err != nil {
		return err
	}
	out.AttendanceTimeline, err = s.repo.AttendanceTimeline(ctx, f)
	return err
}

func (s *ReportsService) performance(ctx context.Context, f domain.ReportFilter, profile academicProfile, out *ReportsBoard) (err error) {
	switch profile.scale {
	case domain.ScaleLiteral:
		if out.ResultTerms, err = s.repo.LiteralTerms(ctx, f); err != nil {
			return err
		}
		f.Subject = out.Subject
		if out.ResultSubjects, err = s.repo.LiteralSubjects(ctx, f); err != nil {
			return err
		}
		if out.Subject != "" && len(out.ResultSubjects) == 0 {
			return domain.ErrNotFound
		}
		f.Subject = ""
		if out.ResultGroups, err = s.repo.LiteralGroups(ctx, f); err != nil {
			return err
		}
		if profile.conduct {
			f.Passing = conductPassing
			out.ConductTerms, err = s.repo.ConductTerms(ctx, f)
		}
		return err
	case domain.ScaleVigesimal:
		if out.ResultSubjects, err = s.repo.VigesimalSubjects(ctx, f); err != nil {
			return err
		}
		out.ResultGroups, err = s.repo.VigesimalGroups(ctx, f)
		return err
	default:
		// Academies: the mock exams play the role of the terms
		if out.ResultTerms, err = s.repo.MockSeries(ctx, f); err != nil {
			return err
		}
		out.ResultSubjects, err = s.repo.MockCourses(ctx, f)
		return err
	}
}

func (s *ReportsService) finance(ctx context.Context, f domain.ReportFilter, out *ReportsBoard) (err error) {
	if out.Finance, err = s.repo.FinanceTotals(ctx, f); err != nil {
		return err
	}
	if out.Months, err = s.repo.FinanceMonths(ctx, f); err != nil {
		return err
	}
	if out.Methods, err = s.repo.FinanceMethods(ctx, f); err != nil {
		return err
	}
	if out.Aging, err = s.repo.FinanceAging(ctx, f); err != nil {
		return err
	}
	out.FinanceByGrp, err = s.repo.FinanceGroups(ctx, f)
	return err
}

// SeedDemoReports fills the attendance of the whole running periods and gives the closed ones marks and paid
// fees, so the board and its comparison have something to show
func SeedDemoReports(ctx context.Context, repo port.ReportsRepository, bySlug func(context.Context, string) (domain.Institution, error), slugs []string) (int, error) {
	total := 0
	for _, slug := range slugs {
		inst, err := bySlug(ctx, slug)
		if err != nil {
			return total, err
		}
		id := inst.ID
		n, err := repo.SeedAttendanceHistory(ctx, id)
		if err != nil {
			return total, err
		}
		total += n
		if n, err = repo.SeedClosedYear(ctx, id); err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}
