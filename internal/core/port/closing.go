package port

import (
	"context"

	"api/internal/core/domain"
)

// FinalRow is what confirming a section stores per student
type FinalRow struct {
	StudentID string
	Computed  string
	Reason    string
}

// ClosingRepository is the year and semester closing in PostgreSQL, scoped to institutionID
type ClosingRepository interface {
	SectionLevel(ctx context.Context, institutionID, sectionID string) (domain.SectionLevel, error)
	FinalResults(ctx context.Context, institutionID, periodID, sectionID string) ([]domain.FinalResult, error)
	// SaveDecision records the team's situation ('' clears it) with its reason
	SaveDecision(ctx context.Context, institutionID, periodID, sectionID, studentID, decided, note, name string) error
	// ConfirmResults stores the computed situations of a section (keeping the team's decisions)
	ConfirmResults(ctx context.Context, institutionID, periodID, sectionID string, rows []FinalRow) error
	RecoveryLevels(ctx context.Context, periodID string, studentIDs []string) (map[string]map[string]string, error)
	SaveRecovery(ctx context.Context, periodID, studentID string, levels map[string]string, after string) error

	UnitResults(ctx context.Context, institutionID, assignmentID string) ([]domain.UnitResult, error)
	ConfirmUnit(ctx context.Context, institutionID, assignmentID string, rows []domain.UnitResult) error
	SaveUnitRecovery(ctx context.Context, assignmentID, studentID string, score *int, final *int, status string) error

	// IssueRecord numbers the acta (per period and kind) and replaces the previous one of the same sheet
	IssueRecord(ctx context.Context, institutionID string, r domain.Record, userID string) (domain.Record, error)
	Records(ctx context.Context, institutionID, periodID string) ([]domain.Record, error)
	Record(ctx context.Context, institutionID, id string) (domain.Record, error)
}
