package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// ReportOption is a level the board can be narrowed to
type ReportOption struct {
	ID   string
	Name string
}

// ReportsRepository reads the aggregates of the direction board. Every query takes the same filter: the period,
// an optional level and, for attendance, the range of days
type ReportsRepository interface {
	Levels(ctx context.Context, institutionID string) ([]ReportOption, error)
	Enrollment(ctx context.Context, f domain.ReportFilter) (domain.EnrollmentStats, error)

	// Attendance: by day, by level (or grade when a level is chosen) and by term (or month without terms)
	AttendanceDays(ctx context.Context, f domain.ReportFilter) ([]domain.AttendanceDay, error)
	AttendanceGroups(ctx context.Context, f domain.ReportFilter) ([]domain.GroupAttendance, error)
	AttendanceTimeline(ctx context.Context, f domain.ReportFilter) ([]domain.GroupAttendance, error)

	// Literal scale (Educación Básica): AD/A/B/C by term, by area and by level or grade, and conduct by term
	LiteralTerms(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error)
	LiteralSubjects(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error)
	LiteralGroups(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error)
	ConductTerms(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error)

	// 0–20 scale (institutes): each student's weighted average by unit, then by unit and by grade
	VigesimalSubjects(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error)
	VigesimalGroups(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error)

	// Score scale (academies): the average of each published mock exam and of each course across them
	MockSeries(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error)
	MockCourses(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error)

	// Collection
	FinanceTotals(ctx context.Context, f domain.ReportFilter) (domain.FinanceTotals, error)
	FinanceMonths(ctx context.Context, f domain.ReportFilter) ([]domain.MoneyFlow, error)
	FinanceMethods(ctx context.Context, f domain.ReportFilter) ([]domain.MethodTotal, error)
	FinanceAging(ctx context.Context, f domain.ReportFilter) ([]domain.AgingBucket, error)
	FinanceGroups(ctx context.Context, f domain.ReportFilter) ([]domain.GroupDebt, error)

	// One line per period that is not in planning, for the comparison
	PeriodKpis(ctx context.Context, institutionID, scale string, passing int, today time.Time) ([]domain.PeriodKpi, error)

	// Demo history: attendance before the seeded weeks, and a closed year with marks and paid fees
	SeedAttendanceHistory(ctx context.Context, institutionID string) (int, error)
	SeedClosedYear(ctx context.Context, institutionID string) (int, error)
}
