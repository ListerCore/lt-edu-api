package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// ProgramsRepository is the programs schema (institutes), scoped to institutionID
type ProgramsRepository interface {
	Programs(ctx context.Context, institutionID string) ([]domain.ProgramInfo, error)
	SetCoordinator(ctx context.Context, institutionID, levelID, staffID string) error
	// Structure of the plan in force of a program: modules and units (with EFSRT), by semester
	Structure(ctx context.Context, institutionID, levelID string) ([]domain.ModuleRef, []domain.UnitRef, error)
	Students(ctx context.Context, institutionID, levelID, query string) ([]domain.ProgramStudent, error)
	Student(ctx context.Context, institutionID, studentID string) (domain.ProgramStudent, error)
	// Standings join the closed units (assessment.unit_results), the history and the units being taken
	Standings(ctx context.Context, institutionID, studentID string) ([]domain.UnitStanding, error)

	Internships(ctx context.Context, f domain.InternshipFilter) ([]domain.Internship, error)
	Internship(ctx context.Context, institutionID, id string) (domain.Internship, error)
	SaveInternship(ctx context.Context, institutionID string, x domain.Internship) (domain.Internship, error)
	DeleteInternship(ctx context.Context, institutionID, id string) error
	Logs(ctx context.Context, internshipID string) ([]domain.InternshipLog, error)
	AddLog(ctx context.Context, internshipID string, l domain.InternshipLog) error
	DeleteLog(ctx context.Context, internshipID, logID string) error
	Evaluate(ctx context.Context, institutionID, id, status string, score int, evaluation, name string, endsOn time.Time) error

	Certificates(ctx context.Context, institutionID, studentID string, levelIDs []string, kind string) ([]domain.Certificate, error)
	Certificate(ctx context.Context, institutionID, id string) (domain.Certificate, error)
	IssueCertificate(ctx context.Context, institutionID string, c domain.Certificate) (domain.Certificate, error)
	AnnulCertificate(ctx context.Context, institutionID, id, reason string) error

	Requirements(ctx context.Context, studentID string) ([]domain.Requirement, error)
	SetRequirement(ctx context.Context, studentID string, r domain.Requirement) error
}
