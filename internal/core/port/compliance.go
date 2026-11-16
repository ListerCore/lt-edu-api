package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// ComplianceRepository is equipment.* and compliance.* in PostgreSQL. Rows of another institution behave as missing
type ComplianceRepository interface {
	Rooms(ctx context.Context, institutionID string) ([]domain.EquipmentRoom, error)
	SaveRoom(ctx context.Context, institutionID string, r domain.EquipmentRoom) (domain.EquipmentRoom, error)
	DeleteRoom(ctx context.Context, institutionID, id string) error
	Devices(ctx context.Context, institutionID string) ([]domain.Device, error)
	SaveDevice(ctx context.Context, institutionID string, d domain.Device) (domain.Device, error)
	DeleteDevice(ctx context.Context, institutionID, id string) error
	Checks(ctx context.Context, institutionID string, limit int) ([]domain.EquipmentCheck, error)
	AddCheck(ctx context.Context, institutionID string, c domain.EquipmentCheck) (domain.EquipmentCheck, error)
	DeleteCheck(ctx context.Context, institutionID, id string) error

	// Report is the data of a report (empty when never saved)
	Report(ctx context.Context, institutionID, kind string) (domain.ComplianceReport, error)
	SaveReport(ctx context.Context, institutionID string, r domain.ComplianceReport) error
	// EnsureItems adds the rows of the matrices that are missing, keeping the ones already there
	EnsureItems(ctx context.Context, institutionID string, items []domain.ComplianceItem) error
	Items(ctx context.Context, institutionID, report string) ([]domain.ComplianceItem, error)
	Item(ctx context.Context, institutionID, id string) (domain.ComplianceItem, error)
	// SaveItem writes module, route, annex, folio and notes
	SaveItem(ctx context.Context, institutionID string, it domain.ComplianceItem) error
	// SetItemStatus sets the state; verified stamps who and when
	SetItemStatus(ctx context.Context, institutionID, id, status, by string, at time.Time) error

	UptimeTargets(ctx context.Context) ([]domain.UptimeTarget, error)
	RecordUptime(ctx context.Context, institutionID string, c domain.UptimeCheck) error
	Uptime(ctx context.Context, institutionID string, since time.Time) ([]domain.UptimeCheck, error)
}

// UptimeProbe asks a URL and measures the answer
type UptimeProbe interface {
	Probe(ctx context.Context, url string) (status int, elapsed time.Duration, err error)
}
