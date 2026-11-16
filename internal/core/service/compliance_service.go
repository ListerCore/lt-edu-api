package service

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// ComplianceService is the DRE compliance of a CEBA (phase E8 of docs/ceba/PLAN.md): the environment, inventory and
// checks of the virtual tutors' equipment (RSG 613-2014-MINEDU, 6.3.2 c), the verification matrices of the two
// technical reports with their evidence (6.2.1 and Anexo 2), the availability log of the platform and the two
// reports in PDF. Dirección, coordinación and the technical lead read it; dirección and the technical lead write;
// only the technical lead verifies a row
type ComplianceService struct {
	repo        port.ComplianceRepository
	institution port.InstitutionRepository
	auth        *AuthService
	files       port.AttachmentRepository
	store       port.ObjectStore
	render      port.DocumentRenderer
	probe       port.UptimeProbe
	live        *LiveService
	audit       *AuditService
	logger      zerolog.Logger
	now         func() time.Time
}

type ComplianceDeps struct {
	Repo        port.ComplianceRepository
	Institution port.InstitutionRepository
	Auth        *AuthService
	Files       port.AttachmentRepository
	Store       port.ObjectStore
	Render      port.DocumentRenderer
	Probe       port.UptimeProbe
	Live        *LiveService
	Audit       *AuditService
	Logger      zerolog.Logger
}

func NewComplianceService(d ComplianceDeps) *ComplianceService {
	return &ComplianceService{repo: d.Repo, institution: d.Institution, auth: d.Auth, files: d.Files, store: d.Store, render: d.Render, probe: d.Probe,
		live: d.Live, audit: d.Audit, logger: d.Logger, now: time.Now}
}

// EventComplianceChanged {institution, area: equipment | items | report | uptime}
const EventComplianceChanged = "compliance.changed"

var (
	complianceReaders = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true, domain.RoleTechnicalLead: true}
	complianceWriters = map[string]bool{domain.RoleSuperadmin: true, "director": true, domain.RoleTechnicalLead: true}
)

type complianceScope struct {
	institutionScope
	userName string
}

func (sc complianceScope) writes() bool { return complianceWriters[sc.role] }

// verifies: the technical lead (the platform superadmin too, who runs the demos)
func (sc complianceScope) verifies() bool {
	return sc.role == domain.RoleTechnicalLead || sc.role == domain.RoleSuperadmin
}

func (s *ComplianceService) scope(ctx context.Context, token, slug string, write bool) (complianceScope, error) {
	isc, err := resolveInstitution(ctx, s.auth, s.institution.InstitutionBySlug, token, slug)
	if err != nil {
		return complianceScope{}, err
	}
	if !complianceReaders[isc.role] {
		return complianceScope{}, domain.ErrComplianceForbidden
	}
	if write && !complianceWriters[isc.role] {
		return complianceScope{}, domain.ErrComplianceReadOnly
	}
	me, err := s.auth.Me(ctx, token)
	if err != nil {
		return complianceScope{}, err
	}
	return complianceScope{institutionScope: isc, userName: uploaderName(me.User)}, nil
}

func (s *ComplianceService) changed(ctx context.Context, sc complianceScope, area string) {
	s.live.Notify(ctx, EventComplianceChanged, map[string]string{"institution": sc.institution.Slug, "area": area})
}

func (s *ComplianceService) record(ctx context.Context, sc complianceScope, e AuditEntry) {
	e.Module = "compliance"
	s.audit.Record(ctx, sc.institutionScope, e)
}

// ---------- the matrices ----------

// defaultItems are the rows of both reports with the module and route of the panel where each element lives
var defaultItems = []domain.ComplianceItem{
	{Report: domain.ReportPlatform, Section: "informative", Code: "enrollment_form", Position: 1, Element: "Ficha única de matrícula del estudiante",
		Module: "Estudiantes › Ficha única (EBA)", Route: "/dashboard/students/[id]?tab=file"},
	{Report: domain.ReportPlatform, Section: "informative", Code: "enrollment_roll", Position: 2, Element: "Nómina de matrícula",
		Module: "Matrícula › Nómina de matrícula", Route: "/dashboard/enrollment/roll"},
	{Report: domain.ReportPlatform, Section: "informative", Code: "permanence", Position: 3, Element: "Registro de asistencia (permanencia de estudiantes)",
		Module: "Asistencia › Permanencia a distancia", Route: "/dashboard/attendance/permanence"},
	{Report: domain.ReportPlatform, Section: "informative", Code: "participation", Position: 4, Element: "Registro de participación del estudiante en las actividades",
		Module: "Aula virtual › Participación", Route: "/dashboard/classroom/[id]/participation"},
	{Report: domain.ReportPlatform, Section: "informative", Code: "evaluation", Position: 5, Element: "Registro del proceso de evaluación de los aprendizajes",
		Module: "Evaluación › Notas por competencias, libretas y cierre", Route: "/dashboard/evaluation/grades"},
	{Report: domain.ReportPlatform, Section: "classroom", Code: "periods_material", Position: 6, Element: "Material educativo estructurado por períodos promocionales",
		Module: "Aula virtual › Unidades y semanas", Route: "/dashboard/classroom/[id]"},
	{Report: domain.ReportPlatform, Section: "classroom", Code: "areas_grades", Position: 7, Element: "Organización por campos del conocimiento y grados",
		Module: "Aula virtual › Aulas por área y grado", Route: "/dashboard/classroom"},
	{Report: domain.ReportPlatform, Section: "classroom", Code: "complementary", Position: 8, Element: "Material educativo complementario",
		Module: "Aula virtual › Material complementario", Route: "/dashboard/classroom/[id]"},
	{Report: domain.ReportPlatform, Section: "classroom", Code: "assessments", Position: 9, Element: "Evaluaciones en el aula virtual",
		Module: "Aula virtual › Evaluaciones en línea", Route: "/dashboard/classroom/[id]/assessments/[itemId]"},
	{Report: domain.ReportPlatform, Section: "classroom", Code: "forums", Position: 10, Element: "Foros",
		Module: "Aula virtual › Foros", Route: "/dashboard/classroom/[id]/forums/[itemId]"},
	{Report: domain.ReportPlatform, Section: "classroom", Code: "videos_links", Position: 11, Element: "Videos y enlaces web educativos",
		Module: "Aula virtual › Videos y enlaces", Route: "/dashboard/classroom/[id]"},
	{Report: domain.ReportPlatform, Section: "classroom", Code: "digital_material", Position: 12, Element: "Material digital y otros recursos educativos",
		Module: "Aula virtual › Biblioteca del CEBA", Route: "/dashboard/classroom/library"},

	{Report: domain.ReportEquipment, Section: "equipment", Code: "devices_exist", Position: 1, Element: "Existencia y ubicación de equipos", Module: "Cumplimiento › Equipos"},
	{Report: domain.ReportEquipment, Section: "equipment", Code: "devices_status", Position: 2, Element: "Estado operativo de equipos", Module: "Cumplimiento › Equipos"},
	{Report: domain.ReportEquipment, Section: "equipment", Code: "devices_assignment", Position: 3, Element: "Asignación a tutores o soporte", Module: "Cumplimiento › Equipos"},
	{Report: domain.ReportEquipment, Section: "equipment", Code: "workspace", Position: 4, Element: "Espacio físico de trabajo", Module: "Cumplimiento › Ambientes"},
	{Report: domain.ReportEquipment, Section: "equipment", Code: "connectivity", Position: 5, Element: "Conectividad a internet", Module: "Cumplimiento › Pruebas"},
	{Report: domain.ReportEquipment, Section: "equipment", Code: "platform_access", Position: 6, Element: "Acceso a plataforma e-learning", Module: "Cumplimiento › Pruebas"},
}

var itemStatuses = map[string]bool{domain.ItemPending: true, domain.ItemVerified: true, domain.ItemNotApplicable: true}

// Board is the whole compliance file of the institution
type ComplianceBoard struct {
	Platform  domain.PlatformSheet
	Reports   []domain.ComplianceReport
	Items     []domain.ComplianceItem
	Rooms     []domain.EquipmentRoom
	Devices   []domain.Device
	Checks    []domain.EquipmentCheck
	Uptime    UptimeSummary
	CanWrite  bool
	CanVerify bool
}

type UptimeSummary struct {
	URL     string
	Checks  int
	Up      int
	Percent float64
	AvgMs   int
	Last    *domain.UptimeCheck
	// Newest first, the last 48
	Recent []domain.UptimeCheck
}

func summarize(url string, list []domain.UptimeCheck) UptimeSummary {
	out := UptimeSummary{URL: url, Checks: len(list)}
	total, timed := 0, 0
	for i, c := range list {
		if c.OK {
			out.Up++
		}
		if c.ResponseMs != nil {
			total += *c.ResponseMs
			timed++
		}
		if i == 0 {
			last := c
			out.Last = &last
		}
	}
	if out.Checks > 0 {
		out.Percent = float64(out.Up) * 100 / float64(out.Checks)
	}
	if timed > 0 {
		out.AvgMs = total / timed
	}
	out.Recent = list[:min(len(list), 48)]
	return out
}

func (s *ComplianceService) Board(ctx context.Context, token, slug string) (ComplianceBoard, error) {
	sc, err := s.scope(ctx, token, slug, false)
	if err != nil {
		return ComplianceBoard{}, err
	}
	inst := sc.institution.ID
	if err := s.repo.EnsureItems(ctx, inst, defaultItems); err != nil {
		return ComplianceBoard{}, err
	}
	b := ComplianceBoard{CanWrite: sc.writes(), CanVerify: sc.verifies()}
	if b.Platform, err = s.institution.PlatformSheet(ctx, inst); err != nil {
		return b, err
	}
	for _, kind := range []string{domain.ReportPlatform, domain.ReportEquipment} {
		r, err := s.report(ctx, inst, kind)
		if err != nil {
			return b, err
		}
		b.Reports = append(b.Reports, r)
		items, err := s.repo.Items(ctx, inst, kind)
		if err != nil {
			return b, err
		}
		b.Items = append(b.Items, items...)
	}
	if b.Rooms, err = s.repo.Rooms(ctx, inst); err != nil {
		return b, err
	}
	if b.Devices, err = s.repo.Devices(ctx, inst); err != nil {
		return b, err
	}
	if b.Checks, err = s.repo.Checks(ctx, inst, 100); err != nil {
		return b, err
	}
	uptime, err := s.repo.Uptime(ctx, inst, s.now().AddDate(0, 0, -30))
	if err != nil {
		return b, err
	}
	b.Uptime = summarize(b.Platform.URL, uptime)
	return b, nil
}

// report fills what was never saved: the file number of the authorizations and the city
func (s *ComplianceService) report(ctx context.Context, institutionID, kind string) (domain.ComplianceReport, error) {
	r, err := s.repo.Report(ctx, institutionID, kind)
	if err != nil || r.UpdatedAt != nil {
		return r, err
	}
	auths, err := s.institution.Authorizations(ctx, institutionID)
	if err != nil {
		return r, err
	}
	for _, a := range auths {
		if a.FileNumber != "" {
			r.FileNumber = a.FileNumber
			break
		}
	}
	r.City = "Ayacucho"
	return r, nil
}

// SaveItem writes where an element lives and its evidence reference (annex and folio)
func (s *ComplianceService) SaveItem(ctx context.Context, token, slug string, in domain.ComplianceItem) (domain.ComplianceItem, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return in, err
	}
	in.Module, in.Route, in.Annex, in.Folio, in.Notes = strings.TrimSpace(in.Module), strings.TrimSpace(in.Route), strings.TrimSpace(in.Annex), strings.TrimSpace(in.Folio), strings.TrimSpace(in.Notes)
	errs := map[string]string{}
	if utf8.RuneCountInString(in.Module) > 200 || utf8.RuneCountInString(in.Route) > 200 {
		errs["module"] = "El módulo y la ruta van hasta 200 caracteres."
	}
	if utf8.RuneCountInString(in.Annex) > 10 || utf8.RuneCountInString(in.Folio) > 10 {
		errs["annex"] = "El anexo y el folio van hasta 10 caracteres."
	}
	if utf8.RuneCountInString(in.Notes) > 500 {
		errs["notes"] = "La nota va hasta 500 caracteres."
	}
	if len(errs) > 0 {
		return in, domain.Validation(errs)
	}
	if err := s.repo.SaveItem(ctx, sc.institution.ID, in); err != nil {
		return in, err
	}
	s.changed(ctx, sc, "items")
	return s.repo.Item(ctx, sc.institution.ID, in.ID)
}

// SetItemStatus marks a row: verified needs the technical lead and at least one evidence uploaded
func (s *ComplianceService) SetItemStatus(ctx context.Context, token, slug, id, status string) (domain.ComplianceItem, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.ComplianceItem{}, err
	}
	if !itemStatuses[status] {
		return domain.ComplianceItem{}, domain.Validation(map[string]string{"status": "Elige pendiente, verificado o no aplica."})
	}
	it, err := s.repo.Item(ctx, sc.institution.ID, id)
	if err != nil {
		return it, err
	}
	if status == domain.ItemVerified {
		if !sc.verifies() {
			return it, domain.ErrVerifyTechLead
		}
		if it.Files == 0 {
			return it, domain.ErrNoEvidence
		}
	}
	if err := s.repo.SetItemStatus(ctx, sc.institution.ID, id, status, sc.userName, s.now()); err != nil {
		return it, err
	}
	s.record(ctx, sc, AuditEntry{Action: domain.AuditStatus, Entity: "compliance_item", EntityID: id,
		Summary: fmt.Sprintf("Marcó «%s» como %s", it.Element, itemStatusNames[status])})
	s.changed(ctx, sc, "items")
	return s.repo.Item(ctx, sc.institution.ID, id)
}

var itemStatusNames = map[string]string{domain.ItemPending: "pendiente", domain.ItemVerified: "verificado", domain.ItemNotApplicable: "no aplica"}

// SaveReport writes the data of a report: number, file, place, evaluation date and conclusions
func (s *ComplianceService) SaveReport(ctx context.Context, token, slug string, r domain.ComplianceReport) (domain.ComplianceReport, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return r, err
	}
	if r.Kind != domain.ReportPlatform && r.Kind != domain.ReportEquipment {
		return r, domain.Validation(map[string]string{"kind": "Informe desconocido."})
	}
	trim := func(v *string, n int, field string, errs map[string]string) {
		*v = strings.TrimSpace(*v)
		if utf8.RuneCountInString(*v) > n {
			errs[field] = fmt.Sprintf("Hasta %d caracteres.", n)
		}
	}
	errs := map[string]string{}
	trim(&r.Number, 30, "number", errs)
	trim(&r.FileNumber, 40, "fileNumber", errs)
	trim(&r.City, 60, "city", errs)
	trim(&r.Location, 300, "location", errs)
	trim(&r.GeneralResult, 1500, "generalResult", errs)
	trim(&r.Limitations, 1500, "limitations", errs)
	trim(&r.Conclusion, 1500, "conclusion", errs)
	if r.EvaluatedOn != nil && r.EvaluatedOn.After(s.now()) {
		errs["evaluatedOn"] = "La evaluación no puede ser futura."
	}
	if len(errs) > 0 {
		return r, domain.Validation(errs)
	}
	r.UpdatedByName = sc.userName
	if err := s.repo.SaveReport(ctx, sc.institution.ID, r); err != nil {
		return r, err
	}
	s.changed(ctx, sc, "report")
	return s.repo.Report(ctx, sc.institution.ID, r.Kind)
}

// ---------- equipment ----------

var (
	equipmentKinds    = map[string]bool{"desktop": true, "laptop": true, "tablet": true, "network": true, "printer": true, "other": true}
	devicePurposes = map[string]bool{"virtual_tutoring": true, "student_support": true, "administration": true, "shared": true}
	deviceStatuses = map[string]bool{"operational": true, "pending": true}
	linkKinds      = map[string]bool{"": true, "fiber": true, "adsl": true, "wireless": true, "mobile": true, "other": true}
)

func (s *ComplianceService) SaveRoom(ctx context.Context, token, slug string, r domain.EquipmentRoom) (domain.EquipmentRoom, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return r, err
	}
	r.Name, r.Location, r.Furniture, r.PowerSupply, r.Notes = strings.TrimSpace(r.Name), strings.TrimSpace(r.Location), strings.TrimSpace(r.Furniture), strings.TrimSpace(r.PowerSupply), strings.TrimSpace(r.Notes)
	errs := map[string]string{}
	if n := utf8.RuneCountInString(r.Name); n < 2 || n > 120 {
		errs["name"] = "Escribe el nombre del ambiente."
	}
	if r.AreaM2 != nil && (*r.AreaM2 <= 0 || *r.AreaM2 > 99999) {
		errs["areaM2"] = "El área es mayor que cero."
	}
	if r.Workstations < 0 || r.Workstations > 500 {
		errs["workstations"] = "Entre 0 y 500 puestos."
	}
	if utf8.RuneCountInString(r.Location) > 200 || utf8.RuneCountInString(r.Furniture) > 500 || utf8.RuneCountInString(r.PowerSupply) > 300 || utf8.RuneCountInString(r.Notes) > 500 {
		errs["notes"] = "Algún texto es demasiado largo."
	}
	if len(errs) > 0 {
		return r, domain.Validation(errs)
	}
	out, err := s.repo.SaveRoom(ctx, sc.institution.ID, r)
	if err != nil {
		return out, err
	}
	s.record(ctx, sc, AuditEntry{Action: map[bool]string{true: domain.AuditCreate, false: domain.AuditUpdate}[r.ID == ""], Entity: "equipment_room", EntityID: out.ID,
		Summary: "Registró el ambiente " + out.Name})
	s.changed(ctx, sc, "equipment")
	return out, nil
}

func (s *ComplianceService) DeleteRoom(ctx context.Context, token, slug, id string) error {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteRoom(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "equipment")
	return nil
}

func (s *ComplianceService) SaveDevice(ctx context.Context, token, slug string, d domain.Device) (domain.Device, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return d, err
	}
	d.Code = strings.ToUpper(strings.TrimSpace(d.Code))
	d.Brand, d.Model, d.Specs, d.AssignedTo, d.Notes = strings.TrimSpace(d.Brand), strings.TrimSpace(d.Model), strings.TrimSpace(d.Specs), strings.TrimSpace(d.AssignedTo), strings.TrimSpace(d.Notes)
	errs := map[string]string{}
	if n := utf8.RuneCountInString(d.Code); n < 2 || n > 20 {
		errs["code"] = "Escribe el código del equipo (PC-01, LAP-01…)."
	}
	if !equipmentKinds[d.Kind] {
		errs["kind"] = "Elige el tipo de equipo."
	}
	if !devicePurposes[d.Purpose] {
		errs["purpose"] = "Elige el destino."
	}
	if !deviceStatuses[d.Status] {
		errs["status"] = "Elige operativo o pendiente."
	}
	if utf8.RuneCountInString(d.Brand) > 80 || utf8.RuneCountInString(d.Model) > 120 || utf8.RuneCountInString(d.Specs) > 300 || utf8.RuneCountInString(d.AssignedTo) > 160 || utf8.RuneCountInString(d.Notes) > 500 {
		errs["specs"] = "Algún texto es demasiado largo."
	}
	if len(errs) > 0 {
		return d, domain.Validation(errs)
	}
	out, err := s.repo.SaveDevice(ctx, sc.institution.ID, d)
	if err != nil {
		return out, err
	}
	s.record(ctx, sc, AuditEntry{Action: map[bool]string{true: domain.AuditCreate, false: domain.AuditUpdate}[d.ID == ""], Entity: "equipment_device", EntityID: out.ID,
		Summary: "Registró el equipo " + out.Code})
	s.changed(ctx, sc, "equipment")
	return out, nil
}

func (s *ComplianceService) DeleteDevice(ctx context.Context, token, slug, id string) error {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteDevice(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "equipment")
	return nil
}

// AddCheck records a connectivity test (of a room or a device) or an access test from a device
func (s *ComplianceService) AddCheck(ctx context.Context, token, slug string, c domain.EquipmentCheck) (domain.EquipmentCheck, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return c, err
	}
	c.Provider, c.Profile, c.Result = strings.TrimSpace(c.Provider), strings.TrimSpace(c.Profile), strings.TrimSpace(c.Result)
	errs := map[string]string{}
	switch c.Kind {
	case "connectivity":
		if c.RoomID == "" && c.DeviceID == "" {
			errs["roomId"] = "Elige el ambiente o el equipo probado."
		}
		if c.Provider == "" {
			errs["provider"] = "Indica el proveedor de internet."
		}
		c.Profile = ""
	case "platform_access":
		if c.DeviceID == "" {
			errs["deviceId"] = "Elige el equipo desde el que se probó."
		}
		if c.Profile == "" {
			errs["profile"] = "Indica el perfil con el que se ingresó (tutor, administrador)."
		}
	default:
		errs["kind"] = "Elige conectividad o acceso a la plataforma."
	}
	if !linkKinds[c.LinkKind] {
		errs["linkKind"] = "Elige el tipo de enlace."
	}
	if c.CheckedOn.IsZero() || c.CheckedOn.After(s.now()) {
		errs["checkedOn"] = "Indica la fecha de la prueba (hoy o antes)."
	}
	for _, v := range []*float64{c.DownloadMbps, c.UploadMbps} {
		if v != nil && (*v < 0 || *v > 100000) {
			errs["downloadMbps"] = "Velocidad no válida."
		}
	}
	if c.LatencyMs != nil && (*c.LatencyMs < 0 || *c.LatencyMs > 60000) {
		errs["latencyMs"] = "Latencia no válida."
	}
	if utf8.RuneCountInString(c.Provider) > 120 || utf8.RuneCountInString(c.Profile) > 60 || utf8.RuneCountInString(c.Result) > 500 {
		errs["result"] = "Algún texto es demasiado largo."
	}
	if len(errs) > 0 {
		return c, domain.Validation(errs)
	}
	c.CheckedByName = sc.userName
	out, err := s.repo.AddCheck(ctx, sc.institution.ID, c)
	if err != nil {
		return out, err
	}
	s.record(ctx, sc, AuditEntry{Action: domain.AuditCreate, Entity: "equipment_check", EntityID: out.ID,
		Summary: fmt.Sprintf("Registró una prueba de %s: %s", map[string]string{"connectivity": "conectividad", "platform_access": "acceso a la plataforma"}[out.Kind],
			map[bool]string{true: "conforme", false: "con observaciones"}[out.Passed])})
	s.changed(ctx, sc, "equipment")
	return out, nil
}

func (s *ComplianceService) DeleteCheck(ctx context.Context, token, slug, id string) error {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteCheck(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "equipment")
	return nil
}

// ---------- availability ----------

// probeOnce asks the URL and stores the answer
func (s *ComplianceService) probeOnce(ctx context.Context, institutionID, url string) (domain.UptimeCheck, error) {
	c := domain.UptimeCheck{URL: url, CheckedAt: s.now()}
	status, elapsed, err := s.probe.Probe(ctx, url)
	if err != nil {
		msg := err.Error()
		if len(msg) > 300 {
			msg = msg[:300]
		}
		c.Error = msg
	} else {
		ms := int(elapsed.Milliseconds())
		c.StatusCode, c.ResponseMs = &status, &ms
		c.OK = status >= 200 && status < 400
	}
	return c, s.repo.RecordUptime(ctx, institutionID, c)
}

// CheckNow probes the platform URL of the institution right away
func (s *ComplianceService) CheckNow(ctx context.Context, token, slug string) (domain.UptimeCheck, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.UptimeCheck{}, err
	}
	sheet, err := s.institution.PlatformSheet(ctx, sc.institution.ID)
	if err != nil {
		return domain.UptimeCheck{}, err
	}
	if !strings.HasPrefix(sheet.URL, "http://") && !strings.HasPrefix(sheet.URL, "https://") {
		return domain.UptimeCheck{}, domain.ErrNoPlatformURL
	}
	c, err := s.probeOnce(ctx, sc.institution.ID, sheet.URL)
	if err == nil {
		s.changed(ctx, sc, "uptime")
	}
	return c, err
}

// RunUptime probes every platform URL each interval until ctx ends (the availability log of the reports)
func (s *ComplianceService) RunUptime(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		targets, err := s.repo.UptimeTargets(ctx)
		if err != nil && ctx.Err() == nil {
			s.logger.Warn().Err(err).Msg("uptime targets")
		}
		for _, t := range targets {
			if _, err := s.probeOnce(ctx, t.InstitutionID, t.URL); err != nil && ctx.Err() == nil {
				s.logger.Warn().Err(err).Str("institution", t.InstitutionID).Msg("uptime record")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// ---------- evidence ----------

// maxEvidenceBytes keeps a report light: bigger images are left out (the file still hangs on the row)
const maxEvidenceBytes = 6 << 20

// evidence reads the images of an owner (PNG or JPEG), oldest first
func (s *ComplianceService) evidence(ctx context.Context, institutionID, kind, id string) ([]port.Image, error) {
	files, err := s.files.List(ctx, institutionID, kind, id)
	if err != nil {
		return nil, err
	}
	var out []port.Image
	for _, f := range files {
		typ := map[string]string{"image/png": "png", "image/jpeg": "jpg"}[f.ContentType]
		if typ == "" || f.SizeBytes > maxEvidenceBytes || s.store == nil || !s.store.Ready() {
			continue
		}
		body, _, err := s.store.Get(ctx, f.ObjectKey)
		if err != nil {
			s.logger.Warn().Err(err).Str("key", f.ObjectKey).Msg("evidence read")
			continue
		}
		data, err := io.ReadAll(io.LimitReader(body, maxEvidenceBytes+1))
		_ = body.Close()
		if err != nil || len(data) > maxEvidenceBytes {
			continue
		}
		out = append(out, port.Image{Data: data, Type: typ})
	}
	return out, nil
}

// itemsDone reports whether every row is verified or not applicable
func itemsDone(items []domain.ComplianceItem) bool {
	return !slices.ContainsFunc(items, func(it domain.ComplianceItem) bool { return it.Status == domain.ItemPending })
}
