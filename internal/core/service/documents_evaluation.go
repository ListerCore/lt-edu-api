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

// Documents of the EBA closing (phase E7 of docs/ceba/PLAN.md): the consolidated acta signed through documents, the
// SIAGIE spreadsheet of the final grades and the certificates (constancias) of enrollment, studies and conclusion of
// a cycle

// recordDoc is the printable acta of an issued record, as frozen when it was issued
func recordDoc(rec domain.Record) (port.RecordDoc, error) {
	var c RecordContent
	if err := json.Unmarshal(rec.Content, &c); err != nil {
		return port.RecordDoc{}, fmt.Errorf("acta %s: %w", rec.Code, err)
	}
	issued := c.IssuedOn
	if t, err := time.Parse(time.DateOnly, c.IssuedOn); err == nil {
		issued = shortDate(t)
	}
	doc := port.RecordDoc{Institution: c.Institution, Title: c.Title, Code: rec.Code, Issued: issued, Replaced: rec.Status == "replaced",
		Subtitle: strings.Join(nonBlank(c.Period, c.Section, c.Level, c.Subject), " · "), Legend: strings.Join(c.Legend, " · "), Signatures: c.Signatures}
	for _, kv := range [][2]string{{"Tutor(a)", c.Tutor}, {"Docente", c.Teacher}, {"Regla aplicada", c.Rule}} {
		if kv[1] != "" {
			doc.Info = append(doc.Info, kv[0]+": "+kv[1])
		}
	}
	for _, g := range c.Groups {
		group := port.RecordGroupDoc{Name: g.Name}
		for _, col := range g.Columns {
			group.Codes = append(group.Codes, col.Code)
			doc.Columns = append(doc.Columns, col.Code+" "+col.Name)
		}
		doc.Groups = append(doc.Groups, group)
	}
	for _, r := range c.Rows {
		doc.Rows = append(doc.Rows, port.RecordRowDoc{N: r.N, Code: r.Code, Grade: r.Grade, Name: r.Name, Note: r.Note, Cells: r.Cells, Situation: r.Situation})
	}
	for _, sm := range c.Summary {
		doc.Summary = append(doc.Summary, fmt.Sprintf("%s: %d", sm.Label, sm.Count))
	}
	return doc, nil
}

func jsonHash(v any) (string, []byte, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), body, nil
}

// signatures turns the signers of a kind into the empty signatures of a new version
func newSignatures(signers []domain.DocumentSigner) []domain.DocumentSignature {
	out := make([]domain.DocumentSignature, 0, len(signers))
	for i, sg := range signers {
		out = append(out, domain.DocumentSignature{Sort: i + 1, StaffID: sg.StaffID, Name: sg.Name, Position: sg.Position})
	}
	return out
}

// ---------- signed acta ----------

// GenerateRecordAct freezes an issued consolidated (or complementary) acta as a version of a document waiting for
// the signatures of its signers. Issuing the acta again and generating it adds a new version of the same document
func (s *DocumentService) GenerateRecordAct(ctx context.Context, token, slug, recordID, note string) (domain.Record, domain.DocumentVersion, error) {
	sc, err := s.closing.ebaScope(ctx, token, slug, true)
	if err != nil {
		return domain.Record{}, domain.DocumentVersion{}, err
	}
	if note = strings.TrimSpace(note); len([]rune(note)) > 300 {
		return domain.Record{}, domain.DocumentVersion{}, domain.Validation(map[string]string{"note": "La nota va hasta 300 caracteres."})
	}
	rec, err := s.closing.repo.Record(ctx, sc.institution.ID, recordID)
	if err != nil {
		return rec, domain.DocumentVersion{}, err
	}
	if rec.Kind != "final" && rec.Kind != "complementary" {
		return rec, domain.DocumentVersion{}, domain.ErrRecordNotFinal
	}
	if rec.Status != "issued" {
		return rec, domain.DocumentVersion{}, domain.ErrRecordReplaced
	}
	signers, err := s.docs.Signers(ctx, sc.institution.ID, domain.DocumentEvaluationRecord)
	if err != nil {
		return rec, domain.DocumentVersion{}, err
	}
	if len(signers) == 0 {
		return rec, domain.DocumentVersion{}, &domain.Error{Kind: domain.KindInvalid, Code: domain.ErrNoSigners.Code, Message: "Primero indica quién firma las actas de evaluación (Firmantes)."}
	}
	doc, err := recordDoc(rec)
	if err != nil {
		return rec, domain.DocumentVersion{}, err
	}
	h := s.enrollmentHeader(ctx, token, slug, sc.academicScope, doc.Title)
	h.Subtitle = "Educación Básica Alternativa · " + doc.Subtitle
	h.Code = "Acta " + rec.Code
	doc.Header, doc.Signatures = &h, nil
	hash, body, err := jsonHash(doc)
	if err != nil {
		return rec, domain.DocumentVersion{}, err
	}
	v := domain.DocumentVersion{VerificationCode: verificationCode(), Content: body, ContentHash: hash, Note: note, CreatedName: s.assessment.userName(sc), Signatures: newSignatures(signers)}
	issued := domain.IssuedDocument{InstitutionID: sc.institution.ID, Kind: domain.DocumentEvaluationRecord, ScopeKey: rec.SectionID + ":" + rec.Kind,
		Title: doc.Title + " · " + rec.SectionLabel + " · " + strings.SplitN(doc.Subtitle, " · ", 2)[0], TrackingCode: randomCode(trackingAlphabet, 12)}
	d, created, _, err := s.docs.CreateVersion(ctx, issued, v, sc.userID)
	if err != nil {
		return rec, domain.DocumentVersion{}, err
	}
	if err := s.closing.repo.LinkRecord(ctx, rec.ID, d.ID); err != nil {
		return rec, created, err
	}
	s.closing.audit(ctx, sc, AuditEntry{Action: domain.AuditCreate, Entity: "evaluation_record", EntityID: rec.ID,
		Summary: "Generó para firma el acta " + rec.Code + " de " + rec.SectionLabel})
	s.closing.changed(ctx, sc, rec.SectionID)
	rec, err = s.closing.repo.Record(ctx, sc.institution.ID, rec.ID)
	return rec, created, err
}

// RecordActPdf is the newest signed version of an acta, with its seal
func (s *DocumentService) RecordActPdf(ctx context.Context, token, slug, recordID string) (PdfFile, error) {
	sc, err := s.closing.ebaScope(ctx, token, slug, false)
	if err != nil {
		return PdfFile{}, err
	}
	rec, err := s.closing.repo.Record(ctx, sc.institution.ID, recordID)
	if err != nil {
		return PdfFile{}, err
	}
	if rec.ActVersionID == "" {
		return PdfFile{}, &domain.Error{Kind: domain.KindNotFound, Code: "no_act", Message: "Genera el acta para firma antes de descargarla."}
	}
	d, v, err := s.docs.Version(ctx, sc.institution.ID, rec.ActVersionID)
	if err != nil {
		return PdfFile{}, err
	}
	return s.renderVersion(d, v)
}

// renderRecord prints a frozen acta version with its seal
func (s *DocumentService) renderRecord(d domain.IssuedDocument, v domain.DocumentVersion) (PdfFile, error) {
	var doc port.RecordDoc
	if err := json.Unmarshal(v.Content, &doc); err != nil {
		return PdfFile{}, fmt.Errorf("version %s: %w", v.ID, err)
	}
	doc.Seal = s.seal(d, v)
	content, err := s.render.Record(doc)
	if err != nil {
		return PdfFile{}, err
	}
	return PdfFile{Name: fmt.Sprintf("%s-v%d.pdf", slugify(d.Title), v.Number), Content: content}, nil
}

// ---------- SIAGIE: final grades ----------

// siagieGradeColumns lead the grades spreadsheet; one column per competency follows, then the situation. The layout
// must be checked against the UGEL's current template (docs/ceba/PLAN.md, D5)
var siagieGradeColumns = []string{"N.°", "Código modular", "Ciclo", "Grado", "Sección", "N.° de documento", "Código del estudiante", "Apellidos y nombres"}

// SiagieGradesXlsx is the spreadsheet of the final levels of a confirmed section, one row per student and one column
// per competency, with a second sheet saying what each column is; with mark the students become exported
func (s *DocumentService) SiagieGradesXlsx(ctx context.Context, token, slug, sectionID string, mark bool) (PdfFile, int, error) {
	sc, err := s.closing.ebaScope(ctx, token, slug, false)
	if err != nil {
		return PdfFile{}, 0, err
	}
	if mark && !siagieWriter(sc) {
		return PdfFile{}, 0, domain.ErrClosingForbidden
	}
	res, err := s.closing.Section(ctx, token, slug, sectionID)
	if err != nil {
		return PdfFile{}, 0, err
	}
	if len(res.Rows) == 0 {
		return PdfFile{}, 0, domain.ErrNothingToRecord
	}
	for _, r := range res.Rows {
		if !r.Confirmed {
			return PdfFile{}, 0, domain.ErrNotConfirmed
		}
	}
	h := s.enrollmentHeader(ctx, token, slug, sc.academicScope, "")
	columns := slices.Clone(siagieGradeColumns)
	widths := []float64{5, 12, 16, 8, 8, 12, 14, 34}
	var legend [][]port.SheetCell
	for i, a := range res.Areas {
		for j, comp := range a.Competencies {
			code := fmt.Sprintf("C%d.%d", i+1, j+1)
			columns = append(columns, code)
			widths = append(widths, 7)
			legend = append(legend, []port.SheetCell{{Text: code, Bold: true}, {Text: a.Name}, {Text: comp.Name}})
		}
	}
	columns = append(columns, "Situación final", "Estado de carga", "Observación")
	widths = append(widths, 22, 12, 30)
	rows := make([][]port.SheetCell, 0, len(res.Rows))
	var pending []string
	for i, r := range res.Rows {
		grade := r.GradeShort
		if grade == "" {
			grade = res.Section.GradeName
		}
		cells := []port.SheetCell{{Text: strconv.Itoa(i + 1)}, {Text: h.ModularCode}, {Text: res.Section.LevelName}, {Text: grade}, {Text: res.Section.SectionName},
			{Text: r.Student.Document}, {Text: r.Student.Code}, {Text: r.Student.Name}}
		for _, a := range res.Areas {
			exempt := r.ReligionExempt && a.SubjectCode == domain.ReligionSubjectCode
			for _, comp := range a.Competencies {
				level := r.Final[comp.ID]
				if v := r.Recovery[comp.ID]; v != "" {
					level = v
				}
				if exempt {
					level = Exempt
				}
				cells = append(cells, port.SheetCell{Text: level, Bold: level == "C"})
			}
		}
		situation := r.Situation
		if r.AfterRecovery != "" {
			situation = r.AfterRecovery
		}
		status := siagieNames[r.SiagieStatus]
		if mark {
			// The file marks them: it already says what they will be
			status = siagieNames[domain.SiagieExported]
		}
		cells = append(cells, port.SheetCell{Text: situationNames[situation]}, port.SheetCell{Text: status}, port.SheetCell{Text: r.SiagieNote})
		if r.SiagieStatus == domain.SiagieObserved && !mark {
			cells[len(cells)-2].Fill = &port.Colour{R: 253, G: 226, B: 200}
		}
		if r.SiagieStatus != domain.SiagieExported {
			pending = append(pending, r.Student.ID)
		}
		rows = append(rows, cells)
	}
	title := res.Section.Label + " · " + res.Section.LevelName
	book := port.Workbook{Title: "Notas SIAGIE " + title, Sheets: []port.Sheet{
		{Name: "Notas", Widths: widths, Filter: true, Tables: []port.SheetTable{{Title: sc.institution.Name + " · Evaluación final " + res.Period.Name + " · " + title, Columns: columns, Rows: rows}}},
		{Name: "Competencias", Widths: []float64{8, 30, 80}, Tables: []port.SheetTable{{Title: "Qué es cada columna (EXO: exonerado de Educación Religiosa)", Columns: []string{"Columna", "Área", "Competencia"}, Rows: legend}}},
	}}
	content, err := s.sheets.Workbook(book)
	if err != nil {
		return PdfFile{}, 0, err
	}
	file := PdfFile{Name: "siagie-notas-" + slugify(title) + "-" + slugify(res.Period.Name) + ".xlsx", Content: content}
	if !mark || len(pending) == 0 {
		return file, 0, nil
	}
	marked, err := s.closing.markSiagie(ctx, sc, res, pending, domain.SiagieExported, "")
	if err != nil {
		return file, 0, err
	}
	s.closing.audit(ctx, sc, AuditEntry{Action: domain.AuditStatus, Entity: "siagie_grades", EntityID: sectionID,
		Summary: fmt.Sprintf("Exportó al SIAGIE las notas de %d %s de %s", marked, plural(marked, "estudiante", "estudiantes"), title)})
	s.closing.changed(ctx, sc, sectionID)
	return file, marked, nil
}

// ---------- certificates ----------

var certificateTitles = map[string]string{domain.CertificateEnrollment: "Constancia de matrícula", domain.CertificateStudies: "Constancia de estudios",
	domain.CertificateCycle: "Constancia de conclusión de ciclo"}

// CertificateBoard is what the student's certificates tab shows: the ones issued, the studies, and the cycles the
// student concluded (the only ones a conclusion certificate can be issued for)
type CertificateBoard struct {
	Certificates []domain.StudentCertificate
	Studies      []domain.StudyRecord
	Concluded    []domain.StudyRecord
	CanManage    bool
}

func concludedCycles(studies []domain.StudyRecord) []domain.StudyRecord {
	var out []domain.StudyRecord
	for _, r := range studies {
		if r.LastGrade && r.Confirmed && r.Situation == SituationPromoted {
			out = append(out, r)
		}
	}
	return out
}

func (s *DocumentService) StudentCertificates(ctx context.Context, token, slug, studentID string) (CertificateBoard, error) {
	sc, err := s.people.ebaScope(ctx, token, slug, false)
	if err != nil {
		return CertificateBoard{}, err
	}
	if _, err := s.people.repo.Student(ctx, sc.institution.ID, studentID); err != nil {
		return CertificateBoard{}, err
	}
	out := CertificateBoard{CanManage: peopleManagers[sc.role]}
	if out.Studies, err = s.people.repo.StudyRecords(ctx, sc.institution.ID, studentID); err != nil {
		return out, err
	}
	out.Concluded = concludedCycles(out.Studies)
	out.Certificates, err = s.people.repo.Certificates(ctx, sc.institution.ID, studentID)
	return out, err
}

func certificateCode(year, number int) string { return fmt.Sprintf("N.° %03d-%d", number, year) }

var enrollmentStatusNames = map[string]string{domain.EnrollmentActive: "Matriculado", domain.EnrollmentWithdrawn: "Retirado",
	domain.EnrollmentTransferred: "Trasladado", domain.EnrollmentCompleted: "Concluido"}

// studyResult is how a period reads in a certificate
func studyResult(r domain.StudyRecord) string {
	switch {
	case r.Status == domain.EnrollmentWithdrawn || r.Status == domain.EnrollmentTransferred:
		return enrollmentStatusNames[r.Status]
	case r.Confirmed && situationNames[r.Situation] != "":
		return situationNames[r.Situation]
	default:
		return "En curso"
	}
}

// certificateDoc is the frozen content of a certificate
func (s *DocumentService) certificateDoc(ctx context.Context, token, slug string, sc academicScope, c domain.StudentCertificate, studies []domain.StudyRecord) (port.FormDoc, error) {
	d, err := s.people.studentDetail(ctx, sc, c.StudentID)
	if err != nil {
		return port.FormDoc{}, err
	}
	st := d.Student
	doc := port.FormDoc{Header: s.enrollmentHeader(ctx, token, slug, sc, certificateTitles[c.Kind])}
	doc.Header.Subtitle = "Educación Básica Alternativa"
	doc.Header.Code = certificateCode(c.Year, c.Number)
	who := "La dirección de " + sc.institution.Name
	if doc.Header.ModularCode != "" {
		who += ", con código modular " + doc.Header.ModularCode
	}
	doc.Blocks = []port.FormBlock{{Title: "Estudiante", Columns: 2, Fields: []port.FormField{
		{Label: "Apellidos y nombres", Value: st.FullName()}, {Label: "Documento", Value: docTypeNames[st.DocumentType] + " " + st.DocumentNumber},
		{Label: "Fecha de nacimiento", Value: optionalDay(st.BirthDate)}, {Label: "Código del estudiante", Value: st.Code}}}}
	var body string
	switch c.Kind {
	case domain.CertificateEnrollment:
		i := slices.IndexFunc(studies, func(r domain.StudyRecord) bool { return r.PeriodID == c.PeriodID })
		r := studies[i]
		doc.Blocks = append(doc.Blocks, port.FormBlock{Title: "Matrícula", Columns: 2, Fields: []port.FormField{
			{Label: "Periodo promocional", Value: r.PeriodName}, {Label: "Ciclo", Value: r.LevelName}, {Label: "Grado", Value: r.GradeName},
			{Label: "Sección", Value: r.SectionName}, {Label: "Forma de atención", Value: modeNames[r.AttentionMode]}, {Label: "Situación", Value: enrollmentStatusNames[r.Status]}}})
		body = fmt.Sprintf("se encuentra matriculado(a) en el %s del %s de la Educación Básica Alternativa, en el %s.", r.GradeName, r.LevelName, lowerFirst(r.PeriodName))
		if r.Status != domain.EnrollmentActive {
			body = fmt.Sprintf("estuvo matriculado(a) en el %s del %s de la Educación Básica Alternativa, en el %s (%s).", r.GradeName, r.LevelName, lowerFirst(r.PeriodName),
				strings.ToLower(enrollmentStatusNames[r.Status]))
		}
	case domain.CertificateStudies:
		block := port.FormBlock{Title: "Estudios", Columns: 1}
		for _, r := range studies {
			block.Fields = append(block.Fields, port.FormField{Label: r.PeriodName, Value: r.GradeName + " del " + r.LevelName + " · " + studyResult(r)})
		}
		doc.Blocks = append(doc.Blocks, block)
		body = "ha realizado en esta institución los estudios de la Educación Básica Alternativa que se detallan, con la situación final de cada periodo promocional."
	case domain.CertificateCycle:
		i := slices.IndexFunc(studies, func(r domain.StudyRecord) bool {
			return r.LevelID == c.LevelID && r.LastGrade && r.Confirmed && r.Situation == SituationPromoted
		})
		r := studies[i]
		doc.Blocks = append(doc.Blocks, port.FormBlock{Title: "Ciclo concluido", Columns: 2, Fields: []port.FormField{
			{Label: "Ciclo", Value: r.LevelName}, {Label: "Último grado", Value: r.GradeName}, {Label: "Periodo promocional", Value: r.PeriodName},
			{Label: "Situación final", Value: situationNames[r.Situation]}}})
		body = fmt.Sprintf("ha concluido el %s de la Educación Básica Alternativa, al ser promovido(a) del %s en el %s.", r.LevelName, r.GradeName, lowerFirst(r.PeriodName))
		if strings.Contains(strings.ToLower(r.LevelName), "avanzado") {
			body += " Con ello concluye la Educación Básica Alternativa, equivalente a la educación secundaria."
		}
	}
	doc.Paragraphs = append(doc.Paragraphs, who+", hace constar que "+st.FullName()+" "+body)
	purpose := "Se expide la presente a solicitud de la parte interesada, para los fines que estime conveniente."
	if c.Purpose != "" {
		purpose = "Se expide la presente a solicitud de la parte interesada, para " + c.Purpose + "."
	}
	doc.Paragraphs = append(doc.Paragraphs, purpose)
	return doc, nil
}

// GenerateCertificate issues a certificate, numbered in its year, as a version waiting for the signatures of its
// signers. Enrollment needs an enrollment in the period; studies, any enrollment; a cycle, the promotion in its
// last grade
func (s *DocumentService) GenerateCertificate(ctx context.Context, token, slug string, in domain.StudentCertificateInput, note string) (domain.StudentCertificate, domain.DocumentVersion, error) {
	sc, err := s.people.ebaScope(ctx, token, slug, true)
	if err != nil {
		return domain.StudentCertificate{}, domain.DocumentVersion{}, err
	}
	in.Purpose, note = strings.TrimSpace(in.Purpose), strings.TrimSpace(note)
	switch {
	case certificateTitles[in.Kind] == "":
		return domain.StudentCertificate{}, domain.DocumentVersion{}, domain.ErrNoCertificateKind
	case len([]rune(in.Purpose)) > 160:
		return domain.StudentCertificate{}, domain.DocumentVersion{}, domain.Validation(map[string]string{"purpose": "El motivo va hasta 160 caracteres."})
	case len([]rune(note)) > 300:
		return domain.StudentCertificate{}, domain.DocumentVersion{}, domain.Validation(map[string]string{"note": "La nota va hasta 300 caracteres."})
	}
	if _, err := s.people.repo.Student(ctx, sc.institution.ID, in.StudentID); err != nil {
		return domain.StudentCertificate{}, domain.DocumentVersion{}, err
	}
	studies, err := s.people.repo.StudyRecords(ctx, sc.institution.ID, in.StudentID)
	if err != nil {
		return domain.StudentCertificate{}, domain.DocumentVersion{}, err
	}
	switch in.Kind {
	case domain.CertificateEnrollment:
		in.LevelID = ""
		if !slices.ContainsFunc(studies, func(r domain.StudyRecord) bool { return r.PeriodID == in.PeriodID }) {
			return domain.StudentCertificate{}, domain.DocumentVersion{}, domain.ErrNotEnrolledThen
		}
	case domain.CertificateStudies:
		in.PeriodID, in.LevelID = "", ""
		if len(studies) == 0 {
			return domain.StudentCertificate{}, domain.DocumentVersion{}, domain.ErrNoStudies
		}
	case domain.CertificateCycle:
		in.PeriodID = ""
		if !slices.ContainsFunc(concludedCycles(studies), func(r domain.StudyRecord) bool { return r.LevelID == in.LevelID }) {
			return domain.StudentCertificate{}, domain.DocumentVersion{}, domain.ErrCycleNotConcluded
		}
	}
	signers, err := s.docs.Signers(ctx, sc.institution.ID, domain.DocumentCertificate)
	if err != nil {
		return domain.StudentCertificate{}, domain.DocumentVersion{}, err
	}
	if len(signers) == 0 {
		return domain.StudentCertificate{}, domain.DocumentVersion{}, &domain.Error{Kind: domain.KindInvalid, Code: domain.ErrNoSigners.Code, Message: "Primero indica quién firma las constancias (Firmantes)."}
	}
	now := s.now().In(lima())
	c, err := s.people.repo.CreateCertificate(ctx, sc.institution.ID, in, now.Year(), s.people.actorName(ctx, token))
	if err != nil {
		return c, domain.DocumentVersion{}, err
	}
	doc, err := s.certificateDoc(ctx, token, slug, sc, c, studies)
	if err != nil {
		return c, domain.DocumentVersion{}, err
	}
	hash, body, err := formHash(doc)
	if err != nil {
		return c, domain.DocumentVersion{}, err
	}
	v := domain.DocumentVersion{VerificationCode: verificationCode(), Content: body, ContentHash: hash, Note: note, CreatedName: s.people.actorName(ctx, token), Signatures: newSignatures(signers)}
	issued := domain.IssuedDocument{InstitutionID: sc.institution.ID, Kind: domain.DocumentCertificate, ScopeKey: c.ID,
		Title: certificateTitles[c.Kind] + " " + certificateCode(c.Year, c.Number) + " · " + c.StudentName, TrackingCode: randomCode(trackingAlphabet, 12)}
	d, created, _, err := s.docs.CreateVersion(ctx, issued, v, sc.userID)
	if err != nil {
		return c, domain.DocumentVersion{}, err
	}
	if err := s.people.repo.LinkCertificate(ctx, c.ID, d.ID); err != nil {
		return c, created, err
	}
	s.people.record(ctx, sc, AuditEntry{Action: domain.AuditCreate, Entity: "certificate", EntityID: c.ID,
		Summary: "Emitió la " + strings.ToLower(certificateTitles[c.Kind]) + " " + certificateCode(c.Year, c.Number) + " de " + c.StudentName})
	s.people.changed(ctx, sc, "certificates")
	c, err = s.people.repo.Certificate(ctx, sc.institution.ID, c.ID)
	return c, created, err
}

// CertificatePdf is the newest version of a certificate, with its seal
func (s *DocumentService) CertificatePdf(ctx context.Context, token, slug, id string) (PdfFile, error) {
	sc, err := s.people.ebaScope(ctx, token, slug, false)
	if err != nil {
		return PdfFile{}, err
	}
	c, err := s.people.repo.Certificate(ctx, sc.institution.ID, id)
	if err != nil {
		return PdfFile{}, err
	}
	if c.VersionID == "" {
		return PdfFile{}, domain.ErrNotFound
	}
	d, v, err := s.docs.Version(ctx, sc.institution.ID, c.VersionID)
	if err != nil {
		return PdfFile{}, err
	}
	return s.renderVersion(d, v)
}

// lowerFirst writes a name inside a sentence ("Periodo promocional 2026" → "periodo promocional 2026")
func lowerFirst(s string) string {
	for i, r := range s {
		return strings.ToLower(string(r)) + s[i+len(string(r)):]
	}
	return s
}
