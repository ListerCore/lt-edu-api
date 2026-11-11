package domain

import "time"

// Reports (phase 12 of docs/plan-academico.md): the direction board with attendance, performance by competency
// or course, collection and the comparison between periods. Everything here is an aggregate, never a student

// ReportFilter narrows every aggregate to a period, optionally a level, and a range of days for attendance
type ReportFilter struct {
	InstitutionID string
	PeriodID      string
	LevelID       string
	From          time.Time
	To            time.Time
	Today         time.Time
	// Lowest passing grade on the 0–20 scale (institutes: 13; conduct: 11)
	Passing int
	// Literal scale: an area name, to break it down by its competencies
	Subject string
}

// StatusCount tallies attendance marks by status
type StatusCount struct {
	Present   int
	Late      int
	Absent    int
	Justified int
}

// Total is every mark counted
func (c StatusCount) Total() int { return c.Present + c.Late + c.Absent + c.Justified }

// Rate is the share of marks where the student came (on time or late), from 0 to 100; -1 when there are no marks
func (c StatusCount) Rate() float64 {
	if c.Total() == 0 {
		return -1
	}
	return float64(c.Present+c.Late) * 100 / float64(c.Total())
}

// AttendanceDay is one school day of the board
type AttendanceDay struct {
	Day time.Time
	StatusCount
}

// GroupAttendance is the attendance of a level, grade, term or month
type GroupAttendance struct {
	Label string
	StatusCount
}

// ResultMix is how a group did: the AD/A/B/C levels on the literal scale, or the average with approved and failed
// students on the 0–20 and score scales
type ResultMix struct {
	Label    string
	AD       int
	A        int
	B        int
	C        int
	Average  float64
	Max      float64
	Approved int
	Failed   int
	Count    int
}

// GroupEnrollment is the enrollment of a level or grade against its seats
type GroupEnrollment struct {
	Label    string
	Active   int
	Capacity int
}

// EnrollmentStats counts the period's enrollments by status and by how the student came
type EnrollmentStats struct {
	Active      int
	Withdrawn   int
	Transferred int
	Completed   int
	New         int
	Continuing  int
	TransferIn  int
	ReEntry     int
	Capacity    int
	Groups      []GroupEnrollment
}

// FinanceTotals is the period's collection
type FinanceTotals struct {
	Expected  Money
	Discounts Money
	Collected Money
	Overdue   Money
	Debtors   int
	Students  int
}

// MoneyFlow is one month: what fell due (net of discounts), how much of it is paid, and what came in that month
type MoneyFlow struct {
	Month     string
	Expected  Money
	Collected Money
	Received  Money
}

// MethodTotal is what came in through one payment method
type MethodTotal struct {
	Method string
	Amount Money
	Count  int
}

// AgingBucket is the overdue debt by how long ago it fell due
type AgingBucket struct {
	Label    string
	Amount   Money
	Students int
}

// GroupDebt is the collection of a level or grade
type GroupDebt struct {
	Label     string
	Expected  Money
	Collected Money
	Overdue   Money
	Debtors   int
}

// PeriodKpi sums up one period for the comparison; a rate is -1 when the period has no data for it
type PeriodKpi struct {
	PeriodID    string
	Name        string
	Status      string
	Students    int
	Attendance  float64
	Achievement float64
	Collection  float64
}

var ErrReportsForbidden = newError(KindForbidden, "reports_forbidden", "Tu rol no puede ver los reportes.")
