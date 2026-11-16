package grpc

import (
	"context"

	"api/internal/adapter/inbound/grpc/gen/peoplev1"
	"api/internal/core/service"
)

// ImportRoll checks or imports the real roll of a period (phase E9)
func (s *peopleServer) ImportRoll(ctx context.Context, req *peoplev1.ImportRollRequest) (*peoplev1.ImportRollResponse, error) {
	rows := make([]service.ImportRow, 0, len(req.GetRows()))
	for _, r := range req.GetRows() {
		rows = append(rows, service.ImportRow{Line: int(r.GetLine()), DocumentType: r.GetDocumentType(), DocumentNumber: r.GetDocumentNumber(), PaternalSurname: r.GetPaternalSurname(),
			MaternalSurname: r.GetMaternalSurname(), GivenNames: r.GetGivenNames(), BirthDate: r.GetBirthDate(), Sex: r.GetSex(), Email: r.GetEmail(), Phone: r.GetPhone(),
			Cycle: r.GetCycle(), Grade: r.GetGrade(), Section: r.GetSection(), Kind: r.GetKind(), Origin: r.GetOrigin()})
	}
	rep, err := s.people.ImportRoll(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId(), rows, req.GetCommit())
	out := &peoplev1.ImportRollResponse{Total: int32(rep.Total), Ready: int32(rep.Ready), Skipped: int32(rep.Skipped), Errors: int32(rep.Errors), Created: int32(rep.Created),
		Enrolled: int32(rep.Enrolled), Committed: rep.Committed, PeriodBefore: int32(rep.PeriodBefore), PeriodAfter: int32(rep.PeriodAfter)}
	for _, r := range rep.Rows {
		out.Rows = append(out.Rows, &peoplev1.ImportResult{Line: int32(r.Line), Status: r.Status, StudentName: r.StudentName, Placement: r.Placement, Known: r.Known, Message: r.Message})
	}
	for _, x := range rep.Sections {
		out.Sections = append(out.Sections, &peoplev1.ImportSection{SectionId: x.SectionID, Label: x.Label, Capacity: int32(x.Capacity), Before: int32(x.Before),
			Incoming: int32(x.Incoming), After: int32(x.After)})
	}
	// A check with errors still answers its report (the rows say what is wrong); the import refuses with a field error
	if err != nil {
		return nil, s.fail("ImportRoll", err)
	}
	return out, nil
}
