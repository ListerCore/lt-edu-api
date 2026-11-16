package grpc

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/peoplev1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// peopleServer exposes service.PeopleService to joseph-app's server
type peopleServer struct {
	peoplev1.UnimplementedPeopleServiceServer
	people *service.PeopleService
	logger zerolog.Logger
}

func (s *peopleServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("people grpc call failed")
	}
	return fromDomain(err)
}

func datePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatDate(*t)
}

// optionalDate parses an optional YYYY-MM-DD; a malformed one is a field error
func optionalDate(field, value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	dates, err := parseDates(map[string]string{field: value})
	if err != nil {
		return nil, err
	}
	t := dates[field]
	return &t, nil
}

func requiredDate(field, value string) (time.Time, error) {
	t, err := optionalDate(field, value)
	if err != nil || t == nil {
		return time.Time{}, err
	}
	return *t, nil
}

func toStudent(st domain.Student) *peoplev1.Student {
	out := &peoplev1.Student{
		Id: st.ID, Code: st.Code, DocumentType: st.DocumentType, DocumentNumber: st.DocumentNumber, GivenNames: st.GivenNames,
		PaternalSurname: st.PaternalSurname, MaternalSurname: st.MaternalSurname, BirthDate: datePtr(st.BirthDate), Sex: st.Sex, Email: st.Email,
		Phone: st.Phone, Address: st.Address, SpecialNeeds: st.SpecialNeeds, Notes: st.Notes, Status: st.Status, Access: st.Access,
		PrimaryGuardianName: st.PrimaryName, PrimaryGuardianPhone: st.PrimaryPhone, GuardianCount: int32(st.GuardianCount),
	}
	if e := st.Enrollment; e != nil {
		out.Enrollment = &peoplev1.EnrollmentSummary{Id: e.ID, PeriodId: e.PeriodID, PeriodName: e.PeriodName, LevelId: e.LevelID, LevelName: e.LevelName,
			GradeId: e.GradeID, GradeName: e.GradeName, GradeShort: e.GradeShort, SectionId: e.SectionID, SectionName: e.SectionName, Shift: e.Shift, Status: e.Status}
	}
	return out
}

func toGuardian(g domain.Guardian) *peoplev1.Guardian {
	return &peoplev1.Guardian{
		Id: g.ID, DocumentType: g.DocumentType, DocumentNumber: g.DocumentNumber, GivenNames: g.GivenNames, Surnames: g.Surnames, Email: g.Email,
		Phone: g.Phone, Occupation: g.Occupation, Access: g.Access, Relationship: g.Relationship, IsPrimary: g.IsPrimary, CanPickUp: g.CanPickUp,
		LivesWith: g.LivesWith, StudentCount: int32(g.StudentCount),
	}
}

func toEnrollment(e domain.Enrollment) *peoplev1.Enrollment {
	units := e.UnitIDs
	if units == nil {
		units = []string{}
	}
	return &peoplev1.Enrollment{
		Id: e.ID, StudentId: e.StudentID, StudentName: e.StudentName, StudentCode: e.StudentCode, StudentDocument: e.StudentDoc, PeriodId: e.PeriodID,
		PeriodName: e.PeriodName, LevelId: e.LevelID, LevelName: e.LevelName, GradeId: e.GradeID, GradeName: e.GradeName, GradeShort: e.GradeShort,
		SectionId: e.SectionID, SectionName: e.SectionName, Shift: e.Shift, AttentionMode: e.AttentionMode, Kind: e.Kind, Status: e.Status,
		EnrolledOn: formatDate(e.EnrolledOn), EndedOn: datePtr(e.EndedOn), Origin: e.Origin, Destination: e.Destination, Notes: e.Notes,
		UnitIds: units, Credits: int32(e.Credits),
	}
}

func toPlacement(p domain.Placement) *peoplev1.Placement {
	return &peoplev1.Placement{Id: p.ID, StudentId: p.StudentID, StudentName: p.StudentName, Kind: p.Kind, LevelId: p.LevelID, LevelName: p.LevelName,
		GradeId: p.GradeID, GradeName: p.GradeName, DecidedOn: formatDate(p.DecidedOn), Reference: p.Reference, Notes: p.Notes,
		Origin: p.Origin, OriginStudies: p.OriginStudies, SubjectIds: p.SubjectIDs, SubjectNames: p.SubjectNames, ActYear: int32(p.ActYear), ActNumber: int32(p.ActNumber),
		ActVersionId: p.ActVersionID, ActStatus: p.ActStatus}
}

func toDetail(d service.StudentDetail) *peoplev1.StudentDetail {
	out := &peoplev1.StudentDetail{Student: toStudent(d.Student), CanManage: d.CanManage}
	for _, g := range d.Guardians {
		out.Guardians = append(out.Guardians, toGuardian(g))
	}
	for _, e := range d.Enrollments {
		out.Enrollments = append(out.Enrollments, toEnrollment(e))
	}
	for _, p := range d.Placements {
		out.Placements = append(out.Placements, toPlacement(p))
	}
	return out
}

func studentInput(in *peoplev1.StudentInput) (domain.StudentInput, error) {
	birth, err := optionalDate("birthDate", in.GetBirthDate())
	if err != nil {
		return domain.StudentInput{}, err
	}
	return domain.StudentInput{
		Code: in.GetCode(), DocumentType: in.GetDocumentType(), DocumentNumber: in.GetDocumentNumber(), GivenNames: in.GetGivenNames(),
		PaternalSurname: in.GetPaternalSurname(), MaternalSurname: in.GetMaternalSurname(), BirthDate: birth, Sex: in.GetSex(), Email: in.GetEmail(),
		Phone: in.GetPhone(), Address: in.GetAddress(), SpecialNeeds: in.GetSpecialNeeds(), Notes: in.GetNotes(), Status: in.GetStatus(),
	}, nil
}

func guardianInput(in *peoplev1.GuardianInput) domain.GuardianInput {
	return domain.GuardianInput{DocumentType: in.GetDocumentType(), DocumentNumber: in.GetDocumentNumber(), GivenNames: in.GetGivenNames(),
		Surnames: in.GetSurnames(), Email: in.GetEmail(), Phone: in.GetPhone(), Occupation: in.GetOccupation()}
}

func guardianLink(in *peoplev1.GuardianLink) domain.GuardianLink {
	return domain.GuardianLink{Relationship: in.GetRelationship(), IsPrimary: in.GetIsPrimary(), CanPickUp: in.GetCanPickUp(), LivesWith: in.GetLivesWith()}
}

// ---------- context and students ----------

func (s *peopleServer) GetPeopleContext(ctx context.Context, req *peoplev1.GetPeopleContextRequest) (*peoplev1.GetPeopleContextResponse, error) {
	c, err := s.people.Context(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetPeopleContext", err)
	}
	return &peoplev1.GetPeopleContextResponse{Context: toContext(c.Academic), CanManage: c.CanManage, Levels: toLevels(c.Levels), Periods: toPeriods(c.Periods)}, nil
}

func (s *peopleServer) ListStudents(ctx context.Context, req *peoplev1.ListStudentsRequest) (*peoplev1.ListStudentsResponse, error) {
	students, total, err := s.people.Students(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.StudentFilter{
		Query: req.GetQuery(), Status: req.GetStatus(), PeriodID: req.GetPeriodId(), LevelID: req.GetLevelId(), GradeID: req.GetGradeId(),
		SectionID: req.GetSectionId(), NotEnrolled: req.GetNotEnrolled(), Limit: int(req.GetLimit()), Offset: int(req.GetOffset()),
	})
	if err != nil {
		return nil, s.fail("ListStudents", err)
	}
	out := &peoplev1.ListStudentsResponse{Total: int32(total)}
	for _, st := range students {
		out.Students = append(out.Students, toStudent(st))
	}
	return out, nil
}

func (s *peopleServer) GetStudent(ctx context.Context, req *peoplev1.GetStudentRequest) (*peoplev1.GetStudentResponse, error) {
	d, err := s.people.Student(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("GetStudent", err)
	}
	return &peoplev1.GetStudentResponse{Detail: toDetail(d)}, nil
}

func (s *peopleServer) CreateStudent(ctx context.Context, req *peoplev1.CreateStudentRequest) (*peoplev1.CreateStudentResponse, error) {
	in, err := studentInput(req.GetStudent())
	if err != nil {
		return nil, s.fail("CreateStudent", err)
	}
	st, err := s.people.CreateStudent(ctx, bearer(ctx), req.GetInstitutionSlug(), in)
	if err != nil {
		return nil, s.fail("CreateStudent", err)
	}
	return &peoplev1.CreateStudentResponse{Student: toStudent(st)}, nil
}

func (s *peopleServer) UpdateStudent(ctx context.Context, req *peoplev1.UpdateStudentRequest) (*peoplev1.UpdateStudentResponse, error) {
	in, err := studentInput(req.GetStudent())
	if err != nil {
		return nil, s.fail("UpdateStudent", err)
	}
	st, err := s.people.UpdateStudent(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), in)
	if err != nil {
		return nil, s.fail("UpdateStudent", err)
	}
	return &peoplev1.UpdateStudentResponse{Student: toStudent(st)}, nil
}

func (s *peopleServer) DeleteStudent(ctx context.Context, req *peoplev1.DeleteStudentRequest) (*peoplev1.DeleteStudentResponse, error) {
	if err := s.people.DeleteStudent(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteStudent", err)
	}
	return &peoplev1.DeleteStudentResponse{}, nil
}

// ---------- guardians ----------

func (s *peopleServer) FindGuardian(ctx context.Context, req *peoplev1.FindGuardianRequest) (*peoplev1.FindGuardianResponse, error) {
	g, err := s.people.FindGuardian(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetDocumentType(), req.GetDocumentNumber())
	if err != nil {
		return nil, s.fail("FindGuardian", err)
	}
	return &peoplev1.FindGuardianResponse{Guardian: toGuardian(g)}, nil
}

func (s *peopleServer) AddGuardian(ctx context.Context, req *peoplev1.AddGuardianRequest) (*peoplev1.AddGuardianResponse, error) {
	d, err := s.people.AddGuardian(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId(), req.GetGuardianId(), guardianInput(req.GetGuardian()), guardianLink(req.GetLink()))
	if err != nil {
		return nil, s.fail("AddGuardian", err)
	}
	return &peoplev1.AddGuardianResponse{Detail: toDetail(d)}, nil
}

func (s *peopleServer) UpdateGuardian(ctx context.Context, req *peoplev1.UpdateGuardianRequest) (*peoplev1.UpdateGuardianResponse, error) {
	d, err := s.people.UpdateGuardian(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId(), req.GetGuardianId(), guardianInput(req.GetGuardian()), guardianLink(req.GetLink()))
	if err != nil {
		return nil, s.fail("UpdateGuardian", err)
	}
	return &peoplev1.UpdateGuardianResponse{Detail: toDetail(d)}, nil
}

func (s *peopleServer) RemoveGuardian(ctx context.Context, req *peoplev1.RemoveGuardianRequest) (*peoplev1.RemoveGuardianResponse, error) {
	d, err := s.people.RemoveGuardian(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId(), req.GetGuardianId())
	if err != nil {
		return nil, s.fail("RemoveGuardian", err)
	}
	return &peoplev1.RemoveGuardianResponse{Detail: toDetail(d)}, nil
}

func (s *peopleServer) GrantAccess(ctx context.Context, req *peoplev1.GrantAccessRequest) (*peoplev1.GrantAccessResponse, error) {
	d, err := s.people.GrantAccess(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId(), req.GetGuardianId())
	if err != nil {
		return nil, s.fail("GrantAccess", err)
	}
	return &peoplev1.GrantAccessResponse{Detail: toDetail(d)}, nil
}

// ---------- enrollment ----------

func (s *peopleServer) GetEnrollmentBoard(ctx context.Context, req *peoplev1.GetEnrollmentBoardRequest) (*peoplev1.GetEnrollmentBoardResponse, error) {
	b, err := s.people.Board(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.EnrollmentFilter{
		PeriodID: req.GetPeriodId(), LevelID: req.GetLevelId(), GradeID: req.GetGradeId(), SectionID: req.GetSectionId(),
		Status: req.GetStatus(), Query: req.GetQuery(), Limit: int(req.GetLimit()), Offset: int(req.GetOffset()),
	})
	if err != nil {
		return nil, s.fail("GetEnrollmentBoard", err)
	}
	out := &peoplev1.GetEnrollmentBoardResponse{Period: toPeriod(b.Period), Sections: toSections(b.Sections), Total: int32(b.Total)}
	for _, o := range b.Occupancy {
		out.Occupancy = append(out.Occupancy, &peoplev1.SectionOccupancy{SectionId: o.SectionID, Active: int32(o.Active), Capacity: int32(o.Capacity)})
	}
	for _, e := range b.Enrollments {
		out.Enrollments = append(out.Enrollments, toEnrollment(e))
	}
	for _, u := range b.Units {
		levelID := u.CurriculumID
		u.CurriculumID = ""
		out.Units = append(out.Units, toSubject(u))
		out.UnitLevelIds = append(out.UnitLevelIds, levelID)
	}
	return out, nil
}

func (s *peopleServer) Enroll(ctx context.Context, req *peoplev1.EnrollRequest) (*peoplev1.EnrollResponse, error) {
	on, err := requiredDate("enrolledOn", req.GetEnrolledOn())
	if err != nil {
		return nil, s.fail("Enroll", err)
	}
	e, err := s.people.Enroll(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.EnrollmentInput{
		StudentID: req.GetStudentId(), PeriodID: req.GetPeriodId(), LevelID: req.GetLevelId(), GradeID: req.GetGradeId(), SectionID: req.GetSectionId(),
		Kind: req.GetKind(), EnrolledOn: on, Origin: req.GetOrigin(), Notes: req.GetNotes(), UnitIDs: req.GetUnitIds(),
	})
	if err != nil {
		return nil, s.fail("Enroll", err)
	}
	return &peoplev1.EnrollResponse{Enrollment: toEnrollment(e)}, nil
}

func (s *peopleServer) UpdateEnrollment(ctx context.Context, req *peoplev1.UpdateEnrollmentRequest) (*peoplev1.UpdateEnrollmentResponse, error) {
	e, err := s.people.UpdateEnrollment(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetGradeId(), req.GetSectionId(), req.GetNotes(), req.GetUnitIds(), req.GetSetUnits())
	if err != nil {
		return nil, s.fail("UpdateEnrollment", err)
	}
	return &peoplev1.UpdateEnrollmentResponse{Enrollment: toEnrollment(e)}, nil
}

func (s *peopleServer) EndEnrollment(ctx context.Context, req *peoplev1.EndEnrollmentRequest) (*peoplev1.EndEnrollmentResponse, error) {
	on, err := requiredDate("endedOn", req.GetEndedOn())
	if err != nil {
		return nil, s.fail("EndEnrollment", err)
	}
	e, err := s.people.EndEnrollment(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetStatus(), on, req.GetDestination())
	if err != nil {
		return nil, s.fail("EndEnrollment", err)
	}
	return &peoplev1.EndEnrollmentResponse{Enrollment: toEnrollment(e)}, nil
}

func (s *peopleServer) DeleteEnrollment(ctx context.Context, req *peoplev1.DeleteEnrollmentRequest) (*peoplev1.DeleteEnrollmentResponse, error) {
	if err := s.people.DeleteEnrollment(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteEnrollment", err)
	}
	return &peoplev1.DeleteEnrollmentResponse{}, nil
}

func (s *peopleServer) RatifyEnrollments(ctx context.Context, req *peoplev1.RatifyEnrollmentsRequest) (*peoplev1.RatifyEnrollmentsResponse, error) {
	r, err := s.people.Ratify(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetFromPeriodId(), req.GetToPeriodId(), req.GetPreview())
	if err != nil {
		return nil, s.fail("RatifyEnrollments", err)
	}
	return &peoplev1.RatifyEnrollmentsResponse{Enrolled: int32(r.Enrolled), WithoutSection: int32(r.WithoutSection), Already: int32(r.Already), Finishing: int32(r.Finishing), Inactive: int32(r.Inactive)}, nil
}

// ---------- placements ----------

func (s *peopleServer) ListPlacements(ctx context.Context, req *peoplev1.ListPlacementsRequest) (*peoplev1.ListPlacementsResponse, error) {
	items, err := s.people.Placements(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("ListPlacements", err)
	}
	out := &peoplev1.ListPlacementsResponse{}
	for _, p := range items {
		out.Placements = append(out.Placements, toPlacement(p))
	}
	return out, nil
}

func (s *peopleServer) CreatePlacement(ctx context.Context, req *peoplev1.CreatePlacementRequest) (*peoplev1.CreatePlacementResponse, error) {
	on, err := requiredDate("decidedOn", req.GetDecidedOn())
	if err != nil {
		return nil, s.fail("CreatePlacement", err)
	}
	p, err := s.people.CreatePlacement(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.PlacementInput{
		StudentID: req.GetStudentId(), Kind: req.GetKind(), LevelID: req.GetLevelId(), GradeID: req.GetGradeId(), DecidedOn: on, Reference: req.GetReference(), Notes: req.GetNotes(),
		Origin: req.GetOrigin(), OriginStudies: req.GetOriginStudies(), SubjectIDs: req.GetSubjectIds(),
	})
	if err != nil {
		return nil, s.fail("CreatePlacement", err)
	}
	return &peoplev1.CreatePlacementResponse{Placement: toPlacement(p)}, nil
}

func (s *peopleServer) DeletePlacement(ctx context.Context, req *peoplev1.DeletePlacementRequest) (*peoplev1.DeletePlacementResponse, error) {
	if err := s.people.DeletePlacement(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeletePlacement", err)
	}
	return &peoplev1.DeletePlacementResponse{}, nil
}
