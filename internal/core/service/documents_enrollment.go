package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// Enrollment documents of the EBA (phase E1): the ficha única de matrícula, the nómina (PDF and spreadsheet), the
// SIAGIE upload spreadsheet and the acta de ubicación o convalidación, signed as an issued document

var (
	tongueNames   = map[string]string{"castellano": "Castellano", "quechua": "Quechua", "aimara": "Aimara", "ashaninka": "Asháninka", "other": "Otra"}
	internetNames = map[string]string{"none": "Sin internet", "mobile": "Datos móviles", "home": "Internet en casa", "public": "Cabina o punto público", "unknown": "No indicó"}
	deviceNames   = map[string]string{"none": "Ninguno", "phone": "Celular", "tablet": "Tableta", "computer": "Computadora", "unknown": "No indicó"}
	modeNames     = map[string]string{"in_person": "Presencial", "blended": "Semipresencial", "distance": "A distancia"}
	fileDocNames  = map[string]string{"dni": "DNI", "study_certificate": "Certificado de estudios", "birth_certificate": "Partida de nacimiento",
		"placement_act": "Acta de ubicación o convalidación", "other": "Otro"}
	docTypeNames = map[string]string{"dni": "DNI", "ce": "Carné de extranjería", "passport": "Pasaporte", "other": "Otro"}
	// SIAGIE wording of how the student enters the period
	enrollSituations = map[string]string{domain.EnrollNew: "Ingresante", domain.EnrollContinuing: "Promovido", domain.EnrollTransferIn: "Trasladado",
		domain.EnrollReEntry: "Reentrante"}
	programNames = map[string]string{domain.EBAProgramPEBAJA: "PEBAJA", domain.EBAProgramLiteracy: "Alfabetización"}
)

func yesNo(b bool) string {
	if b {
		return "Sí"
	}
	return "No"
}

func optionalDay(t *time.Time) string {
	if t == nil {
		return ""
	}
	return shortDate(*t)
}

// enrollmentHeader is the official heading of the institution, for the role reading it
func (s *DocumentService) enrollmentHeader(ctx context.Context, token, slug string, sc academicScope, title string) port.DocHeader {
	h := port.DocHeader{Institution: sc.institution.Name, Title: title}
	if actx, err := s.assessment.academic.Context(ctx, token, slug); err == nil {
		h.Dre, h.Ugel, h.ModularCode, h.Logo = actx.Settings.Dre, actx.Settings.Ugel, actx.Settings.ModularCode, imageOf(actx.Settings.Logo)
	}
	return h
}

// ---------- ficha única ----------

// EnrollmentFormPdf is the ficha única de matrícula of a student, with the enrollment of the period given (or the
// latest one)
func (s *DocumentService) EnrollmentFormPdf(ctx context.Context, token, slug, studentID, periodID string) (PdfFile, error) {
	sc, err := s.people.ebaScope(ctx, token, slug, false)
	if err != nil {
		return PdfFile{}, err
	}
	d, err := s.people.studentDetail(ctx, sc, studentID)
	if err != nil {
		return PdfFile{}, err
	}
	f, err := s.people.repo.EnrollmentFile(ctx, sc.institution.ID, studentID)
	if err != nil {
		return PdfFile{}, err
	}
	st := d.Student
	var e *domain.Enrollment
	for i := range d.Enrollments {
		if periodID == "" || d.Enrollments[i].PeriodID == periodID {
			e = &d.Enrollments[i]
			break
		}
	}
	doc := port.FormDoc{Header: s.enrollmentHeader(ctx, token, slug, sc, "Ficha única de matrícula"), IssuedOn: s.now().In(lima())}
	doc.Header.Subtitle = "Educación Básica Alternativa"
	if e != nil {
		doc.Header.Subtitle += " · " + e.PeriodName
	}
	age := ""
	if st.BirthDate != nil {
		age = strconv.Itoa(domain.AgeOn(*st.BirthDate, s.now())) + " años"
	}
	tongue := tongueNames[f.MotherTongue]
	if f.MotherTongue == "other" {
		tongue = f.MotherTongueOther
	}
	sex := map[string]string{"f": "Mujer", "m": "Hombre"}[st.Sex]
	doc.Blocks = append(doc.Blocks, port.FormBlock{Title: "1. Datos del estudiante", Columns: 2, Fields: []port.FormField{
		{Label: "Apellidos y nombres", Value: st.FullName()}, {Label: "Código", Value: st.Code},
		{Label: "Documento", Value: docTypeNames[st.DocumentType] + " " + st.DocumentNumber}, {Label: "Sexo", Value: sex},
		{Label: "Fecha de nacimiento", Value: optionalDay(st.BirthDate)}, {Label: "Edad", Value: age},
		{Label: "Lengua materna", Value: tongue}, {Label: "Ocupación", Value: f.Occupation},
		{Label: "Teléfono", Value: st.Phone}, {Label: "Correo", Value: st.Email},
		{Label: "Dirección", Value: st.Address}, {Label: "Discapacidad o NEE", Value: strings.Join(nonEmptyStrings(yesNo(st.SpecialNeeds), f.Disability), ": ")},
	}})
	doc.Blocks = append(doc.Blocks, port.FormBlock{Title: "2. Conectividad y forma de atención", Columns: 2, Fields: []port.FormField{
		{Label: "Internet", Value: internetNames[f.Internet]}, {Label: "Dispositivo", Value: deviceNames[f.Device]},
		{Label: "Forma que solicita", Value: modeNames[f.PreferredMode]}, {Label: "Últimos estudios", Value: f.LastStudies},
	}})
	if e != nil {
		section := e.SectionName
		if section == "" {
			section = "Por asignar"
		}
		block := port.FormBlock{Title: "3. Matrícula", Columns: 2, Fields: []port.FormField{
			{Label: "Periodo", Value: e.PeriodName}, {Label: "Fecha de matrícula", Value: shortDate(e.EnrolledOn)},
			{Label: "Ciclo", Value: e.LevelName}, {Label: "Grado", Value: e.GradeName},
			{Label: "Sección", Value: section}, {Label: "Forma de atención", Value: modeNames[e.AttentionMode]},
			{Label: "Situación", Value: enrollSituations[e.Kind]}, {Label: "Procedencia", Value: e.Origin},
		}}
		if rows, err := s.people.repo.Roll(ctx, sc.institution.ID, domain.RollFilter{PeriodID: e.PeriodID, All: true}); err == nil {
			if i := slices.IndexFunc(rows, func(r domain.RollRow) bool { return r.EnrollmentID == e.ID }); i >= 0 {
				block.Fields = append(block.Fields, port.FormField{Label: "Exonerado de Religión", Value: yesNo(rows[i].ReligionExempt)},
					port.FormField{Label: "Ubicación", Value: rows[i].Placement})
			}
		}
		doc.Blocks = append(doc.Blocks, block)
	}
	if len(d.Guardians) > 0 {
		g := d.Guardians[0]
		for _, x := range d.Guardians {
			if x.IsPrimary {
				g = x
			}
		}
		doc.Blocks = append(doc.Blocks, port.FormBlock{Title: "4. Apoderado o contacto", Columns: 2, Fields: []port.FormField{
			{Label: "Nombre", Value: g.GivenNames + " " + g.Surnames}, {Label: "Parentesco", Value: g.Relationship},
			{Label: "Documento", Value: docTypeNames[g.DocumentType] + " " + g.DocumentNumber}, {Label: "Teléfono", Value: g.Phone},
		}})
	}
	docs := []string{}
	for _, k := range f.Documents {
		docs = append(docs, fileDocNames[k])
	}
	doc.Blocks = append(doc.Blocks, port.FormBlock{Title: "5. Documentos presentados", Columns: 1, Fields: []port.FormField{{Label: "Documentos", Value: strings.Join(docs, ", ")}}})
	doc.Paragraphs = []string{"Declaro que los datos consignados son verdaderos. Autorizo el uso de mis datos personales para los fines educativos de la institución (Ley 29733)."}
	doc.Signatures = []string{"Firma del estudiante", "Secretaría", "Dirección"}
	content, err := s.render.Form(doc)
	if err != nil {
		return PdfFile{}, err
	}
	return PdfFile{Name: "ficha-matricula-" + slugify(st.Code+" "+st.PaternalSurname) + ".pdf", Content: content}, nil
}

func nonEmptyStrings(values ...string) []string {
	out := []string{}
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// ---------- nómina ----------

// rollGroupKey groups the nómina by section (or by cycle and grade for those without one)
func rollGroupKey(r domain.RollRow) string {
	return r.LevelName + "|" + r.GradeName + "|" + r.SectionName
}

func rollName(r domain.RollRow) string {
	surnames := r.PaternalSurname
	if r.MaternalSurname != "" {
		surnames += " " + r.MaternalSurname
	}
	return surnames + ", " + r.GivenNames
}

// RollPdf is the nómina de matrícula: one page group per section with its students in alphabetical order
func (s *DocumentService) RollPdf(ctx context.Context, token, slug string, f domain.RollFilter) (PdfFile, error) {
	sc, err := s.people.ebaScope(ctx, token, slug, false)
	if err != nil {
		return PdfFile{}, err
	}
	view, err := s.people.roll(ctx, sc, f)
	if err != nil {
		return PdfFile{}, err
	}
	period, err := s.assessment.academic.repo.Period(ctx, sc.institution.ID, f.PeriodID)
	if err != nil {
		return PdfFile{}, err
	}
	doc := port.TableDoc{Header: s.enrollmentHeader(ctx, token, slug, sc, "Nómina de matrícula"), IssuedOn: s.now().In(lima()),
		Columns:    []string{"N.°", "Código", "Documento", "Apellidos y nombres", "Sexo", "F. nacimiento", "Situación", "Exon. Religión", "Ubicación", "SIAGIE"},
		Widths:     []float64{4, 8, 10, 30, 4, 8, 8, 6, 14, 8},
		Legend:     "Situación según el SIAGIE: Ingresante, Promovido, Trasladado o Reentrante. Exon. Religión: exonerado de Educación Religiosa (Ley 29635).",
		Signatures: []string{"Secretaría", "Dirección"}}
	doc.Header.Subtitle = period.Name
	var group *port.TableGroup
	key := ""
	for _, r := range view.Rows {
		if group == nil || rollGroupKey(r) != key {
			key = rollGroupKey(r)
			section := r.SectionName
			if section == "" {
				section = "sin sección"
			}
			info := []string{r.LevelName, r.GradeName, "Sección " + section}
			if m := modeNames[r.AttentionMode]; m != "" {
				info = append(info, m)
			}
			if p := programNames[r.Program]; p != "" {
				info = append(info, p)
			}
			doc.Groups = append(doc.Groups, port.TableGroup{Title: r.LevelName + " · " + r.GradeName + " · " + section, Info: info})
			group = &doc.Groups[len(doc.Groups)-1]
		}
		sex := strings.ToUpper(r.Sex)
		group.Rows = append(group.Rows, []string{strconv.Itoa(len(group.Rows) + 1), r.StudentCode, r.DocumentNumber, rollName(r), sex, optionalDay(r.BirthDate),
			enrollSituations[r.Kind], yesNo(r.ReligionExempt), r.Placement, siagieNames[r.SiagieStatus]})
	}
	for i := range doc.Groups {
		doc.Groups[i].Total = fmt.Sprintf("Total: %d estudiantes", len(doc.Groups[i].Rows))
	}
	content, err := s.render.Table(doc)
	if err != nil {
		return PdfFile{}, err
	}
	return PdfFile{Name: "nomina-" + slugify(period.Name) + ".pdf", Content: content}, nil
}

var siagieNames = map[string]string{domain.SiagiePending: "Pendiente", domain.SiagieExported: "Exportado", domain.SiagieObserved: "Observado"}

// rollColumns are the columns of the nómina spreadsheet and the SIAGIE upload, in the order of the SIAGIE's
// nómina de matrícula. The layout must be checked against the UGEL's current template (docs/ceba/PLAN.md, D5)
var rollColumns = []string{"N.°", "Código modular", "Ciclo", "Grado", "Sección", "Forma de atención", "Programa", "Tipo de documento", "N.° de documento",
	"Código del estudiante", "Apellido paterno", "Apellido materno", "Nombres", "Sexo", "Fecha de nacimiento", "Lengua materna", "Situación de matrícula",
	"Fecha de matrícula", "Exonerado de Religión", "Discapacidad", "Ubicación o convalidación", "Estado de carga", "Observación"}

func rollCells(i int, modular string, r domain.RollRow) []port.SheetCell {
	sex := map[string]string{"f": "M", "m": "H"}[r.Sex]
	values := []string{strconv.Itoa(i), modular, r.LevelName, r.GradeName, r.SectionName, modeNames[r.AttentionMode], programNames[r.Program],
		strings.ToUpper(r.DocumentType), r.DocumentNumber, r.StudentCode, r.PaternalSurname, r.MaternalSurname, r.GivenNames, sex, optionalDay(r.BirthDate),
		tongueNames[r.MotherTongue], enrollSituations[r.Kind], shortDate(r.EnrolledOn), map[bool]string{true: "S", false: "N"}[r.ReligionExempt],
		map[bool]string{true: "S", false: "N"}[r.SpecialNeeds], r.Placement, siagieNames[r.SiagieStatus], r.SiagieNote}
	out := make([]port.SheetCell, len(values))
	for j, v := range values {
		out[j] = port.SheetCell{Text: v}
	}
	if r.SiagieStatus == domain.SiagieObserved {
		out[21].Fill = &port.Colour{R: 253, G: 226, B: 200}
	}
	return out
}

func rollWidths() []float64 {
	return []float64{5, 12, 16, 10, 8, 16, 12, 10, 12, 12, 16, 16, 20, 6, 12, 12, 16, 12, 10, 10, 26, 12, 30}
}

// RollXlsx is the nómina as a spreadsheet, one row per enrollment with every column of the SIAGIE layout
func (s *DocumentService) RollXlsx(ctx context.Context, token, slug string, f domain.RollFilter) (PdfFile, error) {
	sc, err := s.people.ebaScope(ctx, token, slug, false)
	if err != nil {
		return PdfFile{}, err
	}
	return s.rollBook(ctx, token, slug, sc, f, "nomina")
}

func (s *DocumentService) rollBook(ctx context.Context, token, slug string, sc academicScope, f domain.RollFilter, name string) (PdfFile, error) {
	view, err := s.people.roll(ctx, sc, f)
	if err != nil {
		return PdfFile{}, err
	}
	if len(view.Rows) == 0 {
		return PdfFile{}, domain.ErrNothingToExport
	}
	period, err := s.assessment.academic.repo.Period(ctx, sc.institution.ID, f.PeriodID)
	if err != nil {
		return PdfFile{}, err
	}
	h := s.enrollmentHeader(ctx, token, slug, sc, "")
	rows := make([][]port.SheetCell, 0, len(view.Rows))
	for i, r := range view.Rows {
		rows = append(rows, rollCells(i+1, h.ModularCode, r))
	}
	book := port.Workbook{Title: "Nómina " + period.Name, Sheets: []port.Sheet{{Name: "Nómina", Widths: rollWidths(), Filter: true,
		Tables: []port.SheetTable{{Title: sc.institution.Name + " · Nómina de matrícula " + period.Name, Columns: rollColumns, Rows: rows}}}}}
	content, err := s.sheets.Workbook(book)
	if err != nil {
		return PdfFile{}, err
	}
	return PdfFile{Name: name + "-" + slugify(period.Name) + ".xlsx", Content: content}, nil
}

// SiagieXlsx is the upload spreadsheet for the SIAGIE: the enrollments of the filter (by default the pending ones);
// with mark they become exported once the file is built
func (s *DocumentService) SiagieXlsx(ctx context.Context, token, slug string, f domain.RollFilter, mark bool) (PdfFile, int, error) {
	sc, err := s.people.ebaScope(ctx, token, slug, mark)
	if err != nil {
		return PdfFile{}, 0, err
	}
	file, err := s.rollBook(ctx, token, slug, sc, f, "siagie-matricula")
	if err != nil || !mark {
		return file, 0, err
	}
	view, err := s.people.roll(ctx, sc, f)
	if err != nil {
		return PdfFile{}, 0, err
	}
	ids := []string{}
	for _, r := range view.Rows {
		if r.SiagieStatus != domain.SiagieExported {
			ids = append(ids, r.EnrollmentID)
		}
	}
	marked := 0
	if len(ids) > 0 {
		if marked, err = s.people.repo.SetSiagie(ctx, sc.institution.ID, ids, domain.SiagieExported, "", s.people.actorName(ctx, token), s.now()); err != nil {
			return PdfFile{}, 0, err
		}
		s.people.record(ctx, sc, AuditEntry{Action: domain.AuditStatus, Entity: "siagie_export", EntityID: f.PeriodID,
			Summary: fmt.Sprintf("Exportó %d matrículas al SIAGIE", marked)})
		s.people.changed(ctx, sc, "enrollment")
	}
	return file, marked, nil
}

// ---------- acta de ubicación o convalidación ----------

var actTitles = map[string]string{domain.PlacementTest: "Acta de ubicación", domain.PlacementValidation: "Acta de convalidación",
	domain.PlacementRevalidation: "Acta de revalidación", domain.PlacementEquivalence: "Acta de equivalencia"}

func actCode(year, number int) string { return fmt.Sprintf("N.° %03d-%d", number, year) }

// placementAct is the frozen content of the acta
func (s *DocumentService) placementAct(ctx context.Context, token, slug string, sc academicScope, p domain.Placement, year, number int) (port.FormDoc, error) {
	d, err := s.people.studentDetail(ctx, sc, p.StudentID)
	if err != nil {
		return port.FormDoc{}, err
	}
	st := d.Student
	doc := port.FormDoc{Header: s.enrollmentHeader(ctx, token, slug, sc, actTitles[p.Kind])}
	doc.Header.Subtitle = "Educación Básica Alternativa"
	doc.Header.Code = actCode(year, number)
	doc.Header.Logo = port.Image{}
	doc.Blocks = []port.FormBlock{
		{Title: "1. Estudiante", Columns: 2, Fields: []port.FormField{
			{Label: "Apellidos y nombres", Value: st.FullName()}, {Label: "Documento", Value: docTypeNames[st.DocumentType] + " " + st.DocumentNumber},
			{Label: "Fecha de nacimiento", Value: optionalDay(st.BirthDate)}, {Label: "Código", Value: st.Code}}},
		{Title: "2. Trámite", Columns: 2, Fields: []port.FormField{
			{Label: "Tipo", Value: placementKinds[p.Kind]}, {Label: "Fecha", Value: shortDate(p.DecidedOn)},
			{Label: "Procedencia", Value: p.Origin}, {Label: "Estudios que acredita", Value: p.OriginStudies},
			{Label: "Referencia", Value: p.Reference}}},
		{Title: "3. Resultado", Columns: 1, Fields: []port.FormField{
			{Label: "Ciclo y grado", Value: p.GradeName + " del " + p.LevelName},
			{Label: "Áreas convalidadas", Value: strings.Join(p.SubjectNames, ", ")}}},
	}
	verb := map[string]string{domain.PlacementTest: "con el resultado de la prueba de ubicación", domain.PlacementValidation: "con los estudios acreditados",
		domain.PlacementRevalidation: "con los estudios revalidados", domain.PlacementEquivalence: "con las competencias y certificaciones acreditadas"}[p.Kind]
	doc.Paragraphs = []string{fmt.Sprintf("La dirección de %s, %s y en aplicación del Reglamento de la Educación Básica Alternativa y del cuadro de equivalencias "+
		"aprobado por la RVM N.° 0018-2007-ED, ubica a %s en el %s del %s de la Educación Básica Alternativa.", sc.institution.Name, verb, st.FullName(),
		p.GradeName, p.LevelName)}
	if len(p.SubjectNames) > 0 {
		doc.Paragraphs = append(doc.Paragraphs, "Se dan por convalidadas las áreas señaladas, que el estudiante no vuelve a cursar.")
	}
	if p.Notes != "" {
		doc.Paragraphs = append(doc.Paragraphs, "Observaciones: "+p.Notes)
	}
	return doc, nil
}

func formHash(doc port.FormDoc) (string, []byte, error) {
	body, err := json.Marshal(doc)
	if err != nil {
		return "", nil, err
	}
	doc.IssuedOn = time.Time{}
	plain, err := json.Marshal(doc)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(plain)
	return hex.EncodeToString(sum[:]), body, nil
}

// GeneratePlacementAct freezes the acta of a placement as a version waiting for the signatures of its signers. The
// acta keeps its yearly number across versions; when nothing changed the current version comes back
func (s *DocumentService) GeneratePlacementAct(ctx context.Context, token, slug, placementID, note string) (domain.Placement, domain.DocumentVersion, error) {
	sc, err := s.people.ebaScope(ctx, token, slug, true)
	if err != nil {
		return domain.Placement{}, domain.DocumentVersion{}, err
	}
	if note = strings.TrimSpace(note); len([]rune(note)) > 300 {
		return domain.Placement{}, domain.DocumentVersion{}, domain.Validation(map[string]string{"note": "La nota va hasta 300 caracteres."})
	}
	p, err := s.people.repo.Placement(ctx, sc.institution.ID, placementID)
	if err != nil {
		return p, domain.DocumentVersion{}, err
	}
	signers, err := s.docs.Signers(ctx, sc.institution.ID, domain.DocumentPlacement)
	if err != nil {
		return p, domain.DocumentVersion{}, err
	}
	if len(signers) == 0 {
		return p, domain.DocumentVersion{}, &domain.Error{Kind: domain.KindInvalid, Code: domain.ErrNoSigners.Code, Message: "Primero indica quién firma las actas de ubicación (Firmantes)."}
	}
	year, number, err := s.people.repo.AssignAct(ctx, sc.institution.ID, p.ID, p.DecidedOn.Year())
	if err != nil {
		return p, domain.DocumentVersion{}, err
	}
	doc, err := s.placementAct(ctx, token, slug, sc, p, year, number)
	if err != nil {
		return p, domain.DocumentVersion{}, err
	}
	hash, body, err := formHash(doc)
	if err != nil {
		return p, domain.DocumentVersion{}, err
	}
	v := domain.DocumentVersion{VerificationCode: verificationCode(), Content: body, ContentHash: hash, Note: note, CreatedName: s.people.actorName(ctx, token)}
	for i, sg := range signers {
		v.Signatures = append(v.Signatures, domain.DocumentSignature{Sort: i + 1, StaffID: sg.StaffID, Name: sg.Name, Position: sg.Position})
	}
	issued := domain.IssuedDocument{InstitutionID: sc.institution.ID, Kind: domain.DocumentPlacement, ScopeKey: p.ID,
		Title: actTitles[p.Kind] + " " + actCode(year, number) + " · " + p.StudentName, TrackingCode: randomCode(trackingAlphabet, 12)}
	d, created, _, err := s.docs.CreateVersion(ctx, issued, v, sc.userID)
	if err != nil {
		return p, domain.DocumentVersion{}, err
	}
	if err := s.people.repo.LinkAct(ctx, p.ID, d.ID); err != nil {
		return p, created, err
	}
	s.people.record(ctx, sc, AuditEntry{Action: domain.AuditCreate, Entity: "placement_act", EntityID: p.ID,
		Summary: "Generó el " + strings.ToLower(actTitles[p.Kind]) + " " + actCode(year, number) + " de " + p.StudentName})
	s.people.changed(ctx, sc, "placements")
	p, err = s.people.repo.Placement(ctx, sc.institution.ID, p.ID)
	return p, created, err
}

// PlacementActPdf is the newest version of the acta of a placement, with its seal
func (s *DocumentService) PlacementActPdf(ctx context.Context, token, slug, placementID string) (PdfFile, error) {
	sc, err := s.people.ebaScope(ctx, token, slug, false)
	if err != nil {
		return PdfFile{}, err
	}
	p, err := s.people.repo.Placement(ctx, sc.institution.ID, placementID)
	if err != nil {
		return PdfFile{}, err
	}
	if p.ActVersionID == "" {
		return PdfFile{}, &domain.Error{Kind: domain.KindNotFound, Code: "no_act", Message: "Genera el acta antes de descargarla."}
	}
	d, v, err := s.docs.Version(ctx, sc.institution.ID, p.ActVersionID)
	if err != nil {
		return PdfFile{}, err
	}
	return s.renderVersion(d, v)
}

// renderForm prints a frozen form version (an acta) with its seal
func (s *DocumentService) renderForm(d domain.IssuedDocument, v domain.DocumentVersion) (PdfFile, error) {
	var doc port.FormDoc
	if err := json.Unmarshal(v.Content, &doc); err != nil {
		return PdfFile{}, fmt.Errorf("version %s: %w", v.ID, err)
	}
	doc.IssuedOn = v.CreatedAt.In(lima())
	doc.Seal = s.seal(d, v)
	content, err := s.render.Form(doc)
	if err != nil {
		return PdfFile{}, err
	}
	return PdfFile{Name: fmt.Sprintf("%s-v%d.pdf", slugify(d.Title), v.Number), Content: content}, nil
}
