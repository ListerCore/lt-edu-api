package grpc

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/wellbeingv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// wellbeingServer exposes service.WellbeingService to joseph-app's server
type wellbeingServer struct {
	wellbeingv1.UnimplementedWellbeingServiceServer
	wellbeing *service.WellbeingService
	logger    zerolog.Logger
}

func (s *wellbeingServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("wellbeing grpc call failed")
	}
	return fromDomain(err)
}

// schoolDaysAfter is the deadline n school days after an optional date (empty when there is none)
func schoolDaysAfter(t *time.Time, n int) string {
	if t == nil {
		return ""
	}
	d := *t
	for n > 0 {
		d = d.AddDate(0, 0, 1)
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			n--
		}
	}
	return formatDate(d)
}

func toWellStudent(w domain.WellStudent) *wellbeingv1.WellStudent {
	return &wellbeingv1.WellStudent{Id: w.ID, Code: w.Code, Name: w.Name, Placement: w.Placement, SectionId: w.SectionID, SpecialNeeds: w.SpecialNeeds,
		PendingCommitments: int32(w.PendingCommitments), OpenIncidents: int32(w.OpenIncidents), ActiveCase: w.ActiveCase, HasPlan: w.HasPlan, LastInterview: datePtr(w.LastInterview)}
}

func toWellStudents(list []domain.WellStudent) []*wellbeingv1.WellStudent {
	out := make([]*wellbeingv1.WellStudent, 0, len(list))
	for _, w := range list {
		out = append(out, toWellStudent(w))
	}
	return out
}

func toInterview(x domain.Interview) *wellbeingv1.Interview {
	return &wellbeingv1.Interview{Id: x.ID, StudentId: x.StudentID, StudentName: x.StudentName, Kind: x.Kind, HeldOn: formatDate(x.HeldOn), Topic: x.Topic,
		Notes: x.Notes, Agreements: x.Agreements, AuthorName: x.AuthorName}
}

func toCommitment(c domain.Commitment) *wellbeingv1.Commitment {
	return &wellbeingv1.Commitment{Id: c.ID, StudentId: c.StudentID, StudentName: c.StudentName, Placement: c.Placement, InterviewId: c.InterviewID, Party: c.Party,
		Description: c.Description, DueOn: formatDate(c.DueOn), Status: c.Status, CheckedOn: datePtr(c.CheckedOn), AuthorName: c.AuthorName}
}

func toCase(c domain.PsychCase) *wellbeingv1.PsychCase {
	return &wellbeingv1.PsychCase{Id: c.ID, StudentId: c.StudentID, StudentName: c.StudentName, Placement: c.Placement, Origin: c.Origin, ReferredName: c.ReferredName,
		Reason: c.Reason, Priority: c.Priority, Status: c.Status, OpenedOn: formatDate(c.OpenedOn), ClosedOn: datePtr(c.ClosedOn), PsychologistName: c.PsychologistName,
		SharedNote: c.SharedNote, Sessions: int32(c.Sessions), LastSession: datePtr(c.LastSession)}
}

func toSession(x domain.PsychSession) *wellbeingv1.PsychSession {
	return &wellbeingv1.PsychSession{Id: x.ID, CaseId: x.CaseID, HeldOn: formatDate(x.HeldOn), Kind: x.Kind, Notes: x.Notes, NextSteps: x.NextSteps, AuthorName: x.AuthorName}
}

func toMeasure(m domain.Measure) *wellbeingv1.Measure {
	return &wellbeingv1.Measure{Id: m.ID, StudentId: m.StudentID, IncidentId: m.IncidentID, Kind: m.Kind, Ground: m.Ground, Detail: m.Detail, StartsOn: formatDate(m.StartsOn),
		Days: int32(m.Days), DecidedName: m.DecidedName, NotifiedOn: datePtr(m.NotifiedOn), ReconsiderationOn: datePtr(m.ReconsiderationOn),
		ReconsiderationReason: m.ReconsiderationReason, ReconsiderationBy: m.ReconsiderationBy, ResolvedOn: datePtr(m.ResolvedOn), Outcome: m.Outcome, Resolution: m.Resolution,
		ReconsiderUntil: schoolDaysAfter(m.NotifiedOn, 3), ResolveUntil: schoolDaysAfter(m.ReconsiderationOn, 5)}
}

func toIncident(x domain.Incident) *wellbeingv1.Incident {
	out := &wellbeingv1.Incident{Id: x.ID, StudentId: x.StudentID, StudentName: x.StudentName, Placement: x.Placement, OccurredOn: formatDate(x.OccurredOn), Severity: x.Severity,
		Category: x.Category, Description: x.Description, ReportedName: x.ReportedName, FamilyNotifiedOn: datePtr(x.FamilyNotifiedOn), Status: x.Status, Mine: x.Mine}
	for _, m := range x.Measures {
		out.Measures = append(out.Measures, toMeasure(m))
	}
	return out
}

func toCatalog() []*wellbeingv1.IncidentCategory {
	var out []*wellbeingv1.IncidentCategory
	for _, c := range service.IncidentCatalog() {
		out = append(out, &wellbeingv1.IncidentCategory{Code: c.Code, Label: c.Label, Severity: c.Severity, Crime: c.Crime, Violence: c.Violence})
	}
	return out
}

func toIndividualPlan(p domain.IndividualPlan) *wellbeingv1.IndividualPlan {
	return &wellbeingv1.IndividualPlan{Id: p.ID, StudentId: p.StudentID, StudentName: p.StudentName, Placement: p.Placement, PeriodId: p.PeriodID, PeriodName: p.PeriodName,
		SpecialistId: p.SpecialistID, SpecialistName: p.SpecialistName, Needs: p.Needs, Strengths: p.Strengths, EducationalResponse: p.EducationalResponse,
		CurricularAdaptations: p.CurricularAdaptations, AccessAdaptations: p.AccessAdaptations, EvaluationCriteria: p.EvaluationCriteria, Actors: p.Actors,
		FamilyCommitments: p.FamilyCommitments, Status: p.Status, ApprovedName: p.ApprovedName, ApprovedOn: datePtr(p.ApprovedOn), UpdatedAt: instant(&p.UpdatedAt),
		Therapies: int32(p.Therapies)}
}

func fromIndividualPlan(p *wellbeingv1.IndividualPlan) domain.IndividualPlan {
	return domain.IndividualPlan{ID: p.GetId(), StudentID: p.GetStudentId(), PeriodID: p.GetPeriodId(), SpecialistID: p.GetSpecialistId(), Needs: p.GetNeeds(),
		Strengths: p.GetStrengths(), EducationalResponse: p.GetEducationalResponse(), CurricularAdaptations: p.GetCurricularAdaptations(),
		AccessAdaptations: p.GetAccessAdaptations(), EvaluationCriteria: p.GetEvaluationCriteria(), Actors: p.GetActors(), FamilyCommitments: p.GetFamilyCommitments()}
}

func toTherapy(t domain.TherapySession) *wellbeingv1.TherapySession {
	return &wellbeingv1.TherapySession{Id: t.ID, StudentId: t.StudentID, StudentName: t.StudentName, TherapistName: t.TherapistName, Kind: t.Kind, HeldOn: formatDate(t.HeldOn),
		Minutes: int32(t.Minutes), Attended: t.Attended, Goal: t.Goal, Notes: t.Notes, HomeTasks: t.HomeTasks, Mine: t.Mine}
}

// ---------- students and alerts ----------

func (s *wellbeingServer) ListStudents(ctx context.Context, req *wellbeingv1.ListStudentsRequest) (*wellbeingv1.ListStudentsResponse, error) {
	r, err := s.wellbeing.Students(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetQuery(), req.GetSpecialNeeds())
	if err != nil {
		return nil, s.fail("ListStudents", err)
	}
	return &wellbeingv1.ListStudentsResponse{Students: toWellStudents(r.Students), CanTutor: r.CanTutor, CanReport: r.CanReport, CanDecide: r.CanDecide,
		CanPlan: r.CanPlan, Family: r.Family, Ebe: r.EBE}, nil
}

func (s *wellbeingServer) GetStudentFile(ctx context.Context, req *wellbeingv1.GetStudentFileRequest) (*wellbeingv1.GetStudentFileResponse, error) {
	f, err := s.wellbeing.File(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId())
	if err != nil {
		return nil, s.fail("GetStudentFile", err)
	}
	out := &wellbeingv1.GetStudentFileResponse{Student: toWellStudent(f.Student), CanTutor: f.CanTutor, CanRefer: f.CanRefer, CanReport: f.CanReport,
		CanDecide: f.CanDecide, CanPlan: f.CanPlan, Family: f.Family, Today: formatDate(f.Today), Catalog: toCatalog()}
	for _, x := range f.Interviews {
		out.Interviews = append(out.Interviews, toInterview(x))
	}
	for _, x := range f.Sessions {
		out.Sessions = append(out.Sessions, toTutoringSession(x))
	}
	for _, x := range f.Saanee {
		out.Saanee = append(out.Saanee, toSaanee(x))
	}
	for _, c := range f.Commitments {
		out.Commitments = append(out.Commitments, toCommitment(c))
	}
	for _, x := range f.Incidents {
		out.Incidents = append(out.Incidents, toIncident(x))
	}
	for _, m := range f.Measures {
		out.Measures = append(out.Measures, toMeasure(m))
	}
	for _, c := range f.Conduct {
		out.Conduct = append(out.Conduct, &wellbeingv1.TermConduct{TermName: c.TermName, Score: int32(c.Score), Note: c.Note})
	}
	for _, c := range f.Cases {
		out.Cases = append(out.Cases, toCase(c))
	}
	for _, p := range f.Plans {
		out.Plans = append(out.Plans, toIndividualPlan(p))
	}
	for _, t := range f.Therapies {
		out.Therapies = append(out.Therapies, toTherapy(t))
	}
	return out, nil
}

func (s *wellbeingServer) GetAlerts(ctx context.Context, req *wellbeingv1.GetAlertsRequest) (*wellbeingv1.GetAlertsResponse, error) {
	a, err := s.wellbeing.Alerts(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetAlerts", err)
	}
	out := &wellbeingv1.GetAlertsResponse{Counts: map[string]int32{}, Today: formatDate(a.Today)}
	for k, n := range a.Counts {
		out.Counts[k] = int32(n)
	}
	for _, x := range a.Alerts {
		out.Alerts = append(out.Alerts, &wellbeingv1.WellAlert{Kind: x.Kind, Level: x.Level, StudentId: x.StudentID, StudentName: x.StudentName, Placement: x.Placement,
			Title: x.Title, Detail: x.Detail, Since: datePtr(x.Since), Code: x.Code})
	}
	return out, nil
}

// ---------- tutoring ----------

func (s *wellbeingServer) GetTutoring(ctx context.Context, req *wellbeingv1.GetTutoringRequest) (*wellbeingv1.GetTutoringResponse, error) {
	b, err := s.wellbeing.Tutoring(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetTutoring", err)
	}
	out := &wellbeingv1.GetTutoringResponse{Students: toWellStudents(b.Students), CanTutor: b.CanTutor, Family: b.Family, Today: formatDate(b.Today)}
	for _, c := range b.Due {
		out.Due = append(out.Due, toCommitment(c))
	}
	for _, x := range b.Interviews {
		out.Interviews = append(out.Interviews, toInterview(x))
	}
	return out, nil
}

func (s *wellbeingServer) SaveInterview(ctx context.Context, req *wellbeingv1.SaveInterviewRequest) (*wellbeingv1.SaveInterviewResponse, error) {
	in := req.GetInterview()
	held, err := requiredDate("heldOn", in.GetHeldOn())
	if err != nil {
		return nil, s.fail("SaveInterview", err)
	}
	x, err := s.wellbeing.SaveInterview(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.Interview{ID: in.GetId(), StudentID: in.GetStudentId(), Kind: in.GetKind(),
		HeldOn: held, Topic: in.GetTopic(), Notes: in.GetNotes(), Agreements: in.GetAgreements()})
	if err != nil {
		return nil, s.fail("SaveInterview", err)
	}
	return &wellbeingv1.SaveInterviewResponse{Interview: toInterview(x)}, nil
}

func (s *wellbeingServer) DeleteInterview(ctx context.Context, req *wellbeingv1.DeleteInterviewRequest) (*wellbeingv1.DeleteInterviewResponse, error) {
	if err := s.wellbeing.DeleteInterview(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteInterview", err)
	}
	return &wellbeingv1.DeleteInterviewResponse{}, nil
}

func (s *wellbeingServer) SaveCommitment(ctx context.Context, req *wellbeingv1.SaveCommitmentRequest) (*wellbeingv1.SaveCommitmentResponse, error) {
	in := req.GetCommitment()
	due, err := requiredDate("dueOn", in.GetDueOn())
	if err != nil {
		return nil, s.fail("SaveCommitment", err)
	}
	c, err := s.wellbeing.SaveCommitment(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.Commitment{ID: in.GetId(), StudentID: in.GetStudentId(), InterviewID: in.GetInterviewId(),
		Party: in.GetParty(), Description: in.GetDescription(), DueOn: due})
	if err != nil {
		return nil, s.fail("SaveCommitment", err)
	}
	return &wellbeingv1.SaveCommitmentResponse{Commitment: toCommitment(c)}, nil
}

func (s *wellbeingServer) CheckCommitment(ctx context.Context, req *wellbeingv1.CheckCommitmentRequest) (*wellbeingv1.CheckCommitmentResponse, error) {
	if err := s.wellbeing.CheckCommitment(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetStatus()); err != nil {
		return nil, s.fail("CheckCommitment", err)
	}
	return &wellbeingv1.CheckCommitmentResponse{}, nil
}

func (s *wellbeingServer) DeleteCommitment(ctx context.Context, req *wellbeingv1.DeleteCommitmentRequest) (*wellbeingv1.DeleteCommitmentResponse, error) {
	if err := s.wellbeing.DeleteCommitment(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteCommitment", err)
	}
	return &wellbeingv1.DeleteCommitmentResponse{}, nil
}

// ---------- psychology ----------

func (s *wellbeingServer) Refer(ctx context.Context, req *wellbeingv1.ReferRequest) (*wellbeingv1.ReferResponse, error) {
	c, err := s.wellbeing.Refer(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId(), req.GetReason(), req.GetPriority())
	if err != nil {
		return nil, s.fail("Refer", err)
	}
	return &wellbeingv1.ReferResponse{Case: toCase(c)}, nil
}

func (s *wellbeingServer) GetPsychology(ctx context.Context, req *wellbeingv1.GetPsychologyRequest) (*wellbeingv1.GetPsychologyResponse, error) {
	b, err := s.wellbeing.Psychology(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStatus())
	if err != nil {
		return nil, s.fail("GetPsychology", err)
	}
	out := &wellbeingv1.GetPsychologyResponse{ByStatus: map[string]int32{}, Urgent: int32(b.Urgent), Confidential: b.Confidential, CanManage: b.CanManage}
	for k, n := range b.ByStatus {
		out.ByStatus[k] = int32(n)
	}
	for _, c := range b.Cases {
		out.Cases = append(out.Cases, toCase(c))
	}
	return out, nil
}

func (s *wellbeingServer) GetCase(ctx context.Context, req *wellbeingv1.GetCaseRequest) (*wellbeingv1.GetCaseResponse, error) {
	d, err := s.wellbeing.Case(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("GetCase", err)
	}
	out := &wellbeingv1.GetCaseResponse{Case: toCase(d.Case)}
	for _, x := range d.Sessions {
		out.Sessions = append(out.Sessions, toSession(x))
	}
	return out, nil
}

func (s *wellbeingServer) UpdateCase(ctx context.Context, req *wellbeingv1.UpdateCaseRequest) (*wellbeingv1.UpdateCaseResponse, error) {
	c, err := s.wellbeing.UpdateCase(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.PsychCase{ID: req.GetId(), Status: req.GetStatus(), Priority: req.GetPriority(),
		Reason: req.GetReason(), SharedNote: req.GetSharedNote()})
	if err != nil {
		return nil, s.fail("UpdateCase", err)
	}
	return &wellbeingv1.UpdateCaseResponse{Case: toCase(c)}, nil
}

func (s *wellbeingServer) AddSession(ctx context.Context, req *wellbeingv1.AddSessionRequest) (*wellbeingv1.AddSessionResponse, error) {
	in := req.GetSession()
	held, err := requiredDate("heldOn", in.GetHeldOn())
	if err != nil {
		return nil, s.fail("AddSession", err)
	}
	x, err := s.wellbeing.AddSession(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.PsychSession{CaseID: in.GetCaseId(), HeldOn: held, Kind: in.GetKind(),
		Notes: in.GetNotes(), NextSteps: in.GetNextSteps()})
	if err != nil {
		return nil, s.fail("AddSession", err)
	}
	return &wellbeingv1.AddSessionResponse{Session: toSession(x)}, nil
}

func (s *wellbeingServer) DeleteSession(ctx context.Context, req *wellbeingv1.DeleteSessionRequest) (*wellbeingv1.DeleteSessionResponse, error) {
	if err := s.wellbeing.DeleteSession(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetCaseId(), req.GetId()); err != nil {
		return nil, s.fail("DeleteSession", err)
	}
	return &wellbeingv1.DeleteSessionResponse{}, nil
}

// ---------- discipline ----------

func (s *wellbeingServer) GetDiscipline(ctx context.Context, req *wellbeingv1.GetDisciplineRequest) (*wellbeingv1.GetDisciplineResponse, error) {
	from, err := optionalDate("from", req.GetFrom())
	if err != nil {
		return nil, s.fail("GetDiscipline", err)
	}
	to, err := optionalDate("to", req.GetTo())
	if err != nil {
		return nil, s.fail("GetDiscipline", err)
	}
	b, err := s.wellbeing.Discipline(ctx, bearer(ctx), req.GetInstitutionSlug(), from, to, req.GetSeverity(), req.GetStatus())
	if err != nil {
		return nil, s.fail("GetDiscipline", err)
	}
	out := &wellbeingv1.GetDisciplineResponse{Catalog: toCatalog(), Pending: int32(b.Pending), CanReport: b.CanReport, CanDecide: b.CanDecide, Family: b.Family, Today: formatDate(b.Today)}
	for _, x := range b.Incidents {
		out.Incidents = append(out.Incidents, toIncident(x))
	}
	for _, m := range b.Measures {
		pm := toMeasure(m.Measure)
		pm.StudentName, pm.Placement = m.StudentName, m.Placement
		out.Measures = append(out.Measures, pm)
	}
	return out, nil
}

func (s *wellbeingServer) SaveIncident(ctx context.Context, req *wellbeingv1.SaveIncidentRequest) (*wellbeingv1.SaveIncidentResponse, error) {
	in := req.GetIncident()
	occurred, err := requiredDate("occurredOn", in.GetOccurredOn())
	if err != nil {
		return nil, s.fail("SaveIncident", err)
	}
	notified, err := optionalDate("familyNotifiedOn", in.GetFamilyNotifiedOn())
	if err != nil {
		return nil, s.fail("SaveIncident", err)
	}
	x, err := s.wellbeing.SaveIncident(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.Incident{ID: in.GetId(), StudentID: in.GetStudentId(), OccurredOn: occurred,
		Category: in.GetCategory(), Description: in.GetDescription(), FamilyNotifiedOn: notified, Status: in.GetStatus()})
	if err != nil {
		return nil, s.fail("SaveIncident", err)
	}
	return &wellbeingv1.SaveIncidentResponse{Incident: toIncident(x)}, nil
}

func (s *wellbeingServer) DeleteIncident(ctx context.Context, req *wellbeingv1.DeleteIncidentRequest) (*wellbeingv1.DeleteIncidentResponse, error) {
	if err := s.wellbeing.DeleteIncident(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteIncident", err)
	}
	return &wellbeingv1.DeleteIncidentResponse{}, nil
}

func (s *wellbeingServer) SetIncidentStatus(ctx context.Context, req *wellbeingv1.SetIncidentStatusRequest) (*wellbeingv1.SetIncidentStatusResponse, error) {
	if err := s.wellbeing.SetIncidentStatus(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetStatus()); err != nil {
		return nil, s.fail("SetIncidentStatus", err)
	}
	return &wellbeingv1.SetIncidentStatusResponse{}, nil
}

func (s *wellbeingServer) GetMeasureContext(ctx context.Context, req *wellbeingv1.GetMeasureContextRequest) (*wellbeingv1.GetMeasureContextResponse, error) {
	c, err := s.wellbeing.MeasureContext(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId(), req.GetIncidentId())
	if err != nil {
		return nil, s.fail("GetMeasureContext", err)
	}
	avg := -1.0
	if c.ConductAverage != nil {
		avg = *c.ConductAverage
	}
	return &wellbeingv1.GetMeasureContextResponse{MinorInTerm: int32(c.MinorInTerm), MinorInYear: int32(c.MinorInYear), Suspensions: int32(c.Suspensions),
		Conditional: c.Conditional, Expelled: c.Expelled, ConductAverage: avg, VerySeriousFault: c.VerySeriousFault}, nil
}

func (s *wellbeingServer) AddMeasure(ctx context.Context, req *wellbeingv1.AddMeasureRequest) (*wellbeingv1.AddMeasureResponse, error) {
	in := req.GetMeasure()
	starts, err := optionalDate("startsOn", in.GetStartsOn())
	if err != nil {
		return nil, s.fail("AddMeasure", err)
	}
	notified, err := optionalDate("notifiedOn", in.GetNotifiedOn())
	if err != nil {
		return nil, s.fail("AddMeasure", err)
	}
	m := domain.Measure{StudentID: in.GetStudentId(), IncidentID: in.GetIncidentId(), Kind: in.GetKind(), Ground: in.GetGround(), Detail: in.GetDetail(),
		Days: int(in.GetDays()), NotifiedOn: notified}
	if starts != nil {
		m.StartsOn = *starts
	}
	saved, err := s.wellbeing.AddMeasure(ctx, bearer(ctx), req.GetInstitutionSlug(), m)
	if err != nil {
		return nil, s.fail("AddMeasure", err)
	}
	return &wellbeingv1.AddMeasureResponse{Measure: toMeasure(saved)}, nil
}

func (s *wellbeingServer) NotifyMeasure(ctx context.Context, req *wellbeingv1.NotifyMeasureRequest) (*wellbeingv1.NotifyMeasureResponse, error) {
	on, err := requiredDate("notifiedOn", req.GetNotifiedOn())
	if err != nil {
		return nil, s.fail("NotifyMeasure", err)
	}
	if err := s.wellbeing.NotifyMeasure(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), on); err != nil {
		return nil, s.fail("NotifyMeasure", err)
	}
	return &wellbeingv1.NotifyMeasureResponse{}, nil
}

func (s *wellbeingServer) RequestReconsideration(ctx context.Context, req *wellbeingv1.RequestReconsiderationRequest) (*wellbeingv1.RequestReconsiderationResponse, error) {
	if err := s.wellbeing.RequestReconsideration(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetReason()); err != nil {
		return nil, s.fail("RequestReconsideration", err)
	}
	return &wellbeingv1.RequestReconsiderationResponse{}, nil
}

func (s *wellbeingServer) ResolveReconsideration(ctx context.Context, req *wellbeingv1.ResolveReconsiderationRequest) (*wellbeingv1.ResolveReconsiderationResponse, error) {
	if err := s.wellbeing.ResolveReconsideration(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetOutcome(), req.GetResolution()); err != nil {
		return nil, s.fail("ResolveReconsideration", err)
	}
	return &wellbeingv1.ResolveReconsiderationResponse{}, nil
}

func (s *wellbeingServer) DeleteMeasure(ctx context.Context, req *wellbeingv1.DeleteMeasureRequest) (*wellbeingv1.DeleteMeasureResponse, error) {
	if err := s.wellbeing.DeleteMeasure(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteMeasure", err)
	}
	return &wellbeingv1.DeleteMeasureResponse{}, nil
}

// ---------- special education ----------

func (s *wellbeingServer) ListPlans(ctx context.Context, req *wellbeingv1.ListPlansRequest) (*wellbeingv1.ListPlansResponse, error) {
	b, err := s.wellbeing.Plans(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId())
	if err != nil {
		return nil, s.fail("ListPlans", err)
	}
	out := &wellbeingv1.ListPlansResponse{Students: toWellStudents(b.Students), PeriodId: b.PeriodID, PeriodName: b.PeriodName, CanManage: b.CanManage, CanWrite: b.CanWrite}
	for _, p := range b.Plans {
		out.Plans = append(out.Plans, toIndividualPlan(p))
	}
	for _, st := range b.Specialists {
		out.Specialists = append(out.Specialists, &wellbeingv1.StaffRef{Id: st.ID, Name: st.GivenNames + " " + st.Surnames, Position: st.Position})
	}
	return out, nil
}

func (s *wellbeingServer) GetPlan(ctx context.Context, req *wellbeingv1.GetPlanRequest) (*wellbeingv1.GetPlanResponse, error) {
	p, err := s.wellbeing.Plan(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("GetPlan", err)
	}
	name, err := s.wellbeing.InstitutionName(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetPlan", err)
	}
	return &wellbeingv1.GetPlanResponse{Plan: toIndividualPlan(p), Institution: name}, nil
}

func (s *wellbeingServer) SavePlan(ctx context.Context, req *wellbeingv1.SavePlanRequest) (*wellbeingv1.SavePlanResponse, error) {
	p, err := s.wellbeing.SavePlan(ctx, bearer(ctx), req.GetInstitutionSlug(), fromIndividualPlan(req.GetPlan()))
	if err != nil {
		return nil, s.fail("SavePlan", err)
	}
	return &wellbeingv1.SavePlanResponse{Plan: toIndividualPlan(p)}, nil
}

func (s *wellbeingServer) SetPlanStatus(ctx context.Context, req *wellbeingv1.SetPlanStatusRequest) (*wellbeingv1.SetPlanStatusResponse, error) {
	p, err := s.wellbeing.SetPlanStatus(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetStatus())
	if err != nil {
		return nil, s.fail("SetPlanStatus", err)
	}
	return &wellbeingv1.SetPlanStatusResponse{Plan: toIndividualPlan(p)}, nil
}

func (s *wellbeingServer) DeletePlan(ctx context.Context, req *wellbeingv1.DeletePlanRequest) (*wellbeingv1.DeletePlanResponse, error) {
	if err := s.wellbeing.DeletePlan(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeletePlan", err)
	}
	return &wellbeingv1.DeletePlanResponse{}, nil
}

func (s *wellbeingServer) ListTherapies(ctx context.Context, req *wellbeingv1.ListTherapiesRequest) (*wellbeingv1.ListTherapiesResponse, error) {
	from, err := optionalDate("from", req.GetFrom())
	if err != nil {
		return nil, s.fail("ListTherapies", err)
	}
	to, err := optionalDate("to", req.GetTo())
	if err != nil {
		return nil, s.fail("ListTherapies", err)
	}
	l, err := s.wellbeing.Therapies(ctx, bearer(ctx), req.GetInstitutionSlug(), from, to, req.GetStudentId())
	if err != nil {
		return nil, s.fail("ListTherapies", err)
	}
	out := &wellbeingv1.ListTherapiesResponse{Students: toWellStudents(l.Students), CanWrite: l.CanWrite, Family: l.Family}
	for _, t := range l.Sessions {
		out.Sessions = append(out.Sessions, toTherapy(t))
	}
	return out, nil
}

func (s *wellbeingServer) SaveTherapy(ctx context.Context, req *wellbeingv1.SaveTherapyRequest) (*wellbeingv1.SaveTherapyResponse, error) {
	in := req.GetSession()
	held, err := requiredDate("heldOn", in.GetHeldOn())
	if err != nil {
		return nil, s.fail("SaveTherapy", err)
	}
	t, err := s.wellbeing.SaveTherapy(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.TherapySession{ID: in.GetId(), StudentID: in.GetStudentId(), Kind: in.GetKind(),
		HeldOn: held, Minutes: int(in.GetMinutes()), Attended: in.GetAttended(), Goal: in.GetGoal(), Notes: in.GetNotes(), HomeTasks: in.GetHomeTasks()})
	if err != nil {
		return nil, s.fail("SaveTherapy", err)
	}
	return &wellbeingv1.SaveTherapyResponse{Session: toTherapy(t)}, nil
}

func (s *wellbeingServer) DeleteTherapy(ctx context.Context, req *wellbeingv1.DeleteTherapyRequest) (*wellbeingv1.DeleteTherapyResponse, error) {
	if err := s.wellbeing.DeleteTherapy(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteTherapy", err)
	}
	return &wellbeingv1.DeleteTherapyResponse{}, nil
}
