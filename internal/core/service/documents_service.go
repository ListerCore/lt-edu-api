package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// DocumentService builds the printable PDF documents on the server: report cards (SIAGIE layout in Educación
// Básica), timetables for publication, issued actas and payment receipts. Each document goes through the service
// that owns its data, so the same permissions apply as on screen
type DocumentService struct {
	render     port.DocumentRenderer
	sheets     port.WorkbookRenderer
	docs       port.DocumentRepository
	staff      port.StaffRepository
	assessment *AssessmentService
	closing    *ClosingService
	teaching   *TeachingService
	finance    *FinanceService
	// Public URL of joseph-app, where the QR of a document leads
	siteURL string
	now     func() time.Time
}

// DocumentDeps are the outbound adapters the documents need
type DocumentDeps struct {
	Render  port.DocumentRenderer
	Sheets  port.WorkbookRenderer
	Docs    port.DocumentRepository
	Staff   port.StaffRepository
	SiteURL string
}

func NewDocumentService(deps DocumentDeps, assessment *AssessmentService, closing *ClosingService, teaching *TeachingService, finance *FinanceService) *DocumentService {
	return &DocumentService{render: deps.Render, sheets: deps.Sheets, docs: deps.Docs, staff: deps.Staff, siteURL: deps.SiteURL, now: time.Now,
		assessment: assessment, closing: closing, teaching: teaching, finance: finance}
}

// PdfFile is a generated document and the name it downloads with
type PdfFile struct {
	Name    string
	Content []byte
}

var (
	yearPattern    = regexp.MustCompile(`\d{4}`)
	dataURLPattern = regexp.MustCompile(`^data:image/(png|jpeg);base64,(.+)$`)
)

// imageOf decodes a logo data URL; WebP and SVG logos are left out (the PDF library takes PNG and JPEG)
func imageOf(dataURL string) port.Image {
	m := dataURLPattern.FindStringSubmatch(dataURL)
	if m == nil {
		return port.Image{}
	}
	data, err := base64.StdEncoding.DecodeString(m[2])
	if err != nil {
		return port.Image{}
	}
	kind := "png"
	if m[1] == "jpeg" {
		kind = "jpg"
	}
	return port.Image{Data: data, Type: kind}
}

func shortDate(t time.Time) string { return t.Format("02/01/2006") }

// ---------- report cards ----------

// ReportCards is the PDF of one student's card (studentID) or of every card of a section
func (s *DocumentService) ReportCards(ctx context.Context, token, slug, sectionID, termID, studentID, periodID string) (PdfFile, error) {
	var cards []ReportCard
	if studentID != "" {
		c, err := s.assessment.ReportCard(ctx, token, slug, studentID, periodID, termID)
		if err != nil {
			return PdfFile{}, err
		}
		cards = []ReportCard{c}
	} else {
		var err error
		if cards, err = s.assessment.SectionCards(ctx, token, slug, sectionID, termID); err != nil {
			return PdfFile{}, err
		}
	}
	if len(cards) == 0 {
		return PdfFile{}, domain.ErrNotFound
	}
	docs := make([]port.ReportCardDoc, len(cards))
	for i, c := range cards {
		docs[i] = cardDoc(c)
	}
	content, err := s.render.ReportCards(docs)
	if err != nil {
		return PdfFile{}, err
	}
	first := cards[0]
	who := slugify(first.Section.Label)
	if studentID != "" {
		who = slugify(first.Student.Name)
	}
	term := ""
	for _, t := range first.Terms {
		if t.ID == first.TermID {
			term = "-" + slugify(t.Name)
		}
	}
	return PdfFile{Name: fmt.Sprintf("libreta-%s%s.pdf", who, term), Content: content}, nil
}

// cardDoc turns a report card into its printable form
func cardDoc(c ReportCard) port.ReportCardDoc {
	year := yearPattern.FindString(c.PeriodName)
	if year == "" {
		year = c.PeriodName
	}
	grade := c.Section.GradeName
	if grade == "" {
		grade = c.Section.Label
	}
	d := port.ReportCardDoc{Literal: c.Scale == domain.ScaleLiteral, Institution: c.Institution, Dre: c.Dre, Ugel: c.Ugel, ModularCode: c.ModularCode, Logo: imageOf(c.Logo),
		Year: year, Period: c.PeriodName, Level: c.Section.LevelName, Grade: grade, Section: c.Section.SectionName, Student: c.Student.Name, Code: c.Student.Code,
		Document: c.Student.Document, Tutor: c.Section.TutorName, Provisional: c.Provisional, IssuedOn: c.IssuedOn, Passing: c.Passing != nil}
	count := max(c.TermCount, len(c.Terms), 1)
	index := map[string]int{}
	for i := range count {
		if i < len(c.Terms) {
			t := c.Terms[i]
			index[t.ID] = i
			d.Terms = append(d.Terms, port.CardTermDoc{Name: t.Name, Comment: t.Comment, Justified: t.Attendance.Justified, Absent: t.Attendance.Absent, Late: t.Attendance.Late, Conduct: t.Conduct})
			if t.ID == c.TermID {
				d.TermNumber, d.TermName = i+1, t.Name
			}
		} else {
			d.Terms = append(d.Terms, port.CardTermDoc{Empty: true})
		}
	}
	if !d.Literal {
		numericSubjects(c, &d)
		return d
	}
	last := ""
	if len(c.Terms) > 0 {
		last = c.Terms[len(c.Terms)-1].ID
	}
	competencies := func(items []CardCompetency) ([]port.CardCompetencyDoc, string) {
		var out []port.CardCompetencyDoc
		var finals []string
		for _, cc := range items {
			row := port.CardCompetencyDoc{Name: cc.Name, Levels: make([]string, count)}
			for tid, level := range cc.Levels {
				if i, ok := index[tid]; ok {
					row.Levels[i] = level
				}
			}
			// Annual columns only once the card is the period's last term
			if c.Final {
				row.Annual = cc.Levels[last]
				if text := cc.Conclusions[last]; text != "" {
					finals = append(finals, text)
				}
			}
			out = append(out, row)
			if text := cc.Conclusions[c.TermID]; text != "" {
				d.Conclusions = append(d.Conclusions, port.CardConclusionDoc{Competency: cc.Name, Text: text})
			}
		}
		return out, strings.Join(finals, " ")
	}
	for _, sub := range c.Subjects {
		items, final := competencies(sub.Competencies)
		d.Areas = append(d.Areas, port.CardAreaDoc{Name: sub.Name, Competencies: items, FinalConclusion: final})
	}
	if len(c.Transversal) > 0 {
		items, final := competencies(c.Transversal)
		d.Areas = append(d.Areas, port.CardAreaDoc{Name: "Competencias transversales", Transversal: true, Competencies: items, FinalConclusion: final})
	}
	return d
}

// numericSubjects fills the grades list of institutes and academies: the selected term's average of each subject
func numericSubjects(c ReportCard, d *port.ReportCardDoc) {
	d.SubjectHead = "Curso"
	if c.Kind == domain.KindInstitute {
		d.SubjectHead = "Unidad didáctica"
	}
	for _, sub := range c.Subjects {
		row := port.CardSubjectDoc{Name: sub.Name, Teacher: sub.TeacherName, Grade: "—", Situation: "Sin notas"}
		var evals []string
		for _, e := range sub.Evaluations {
			score := "—"
			if e.Score != nil {
				score = strconv.Itoa(*e.Score)
			}
			evals = append(evals, e.Name+": "+score)
		}
		row.Evaluations = strings.Join(evals, " · ")
		if avg := sub.Averages[c.TermID]; avg != nil {
			row.Average = strconv.FormatFloat(*avg, 'f', 1, 64)
			row.Grade = row.Average
			if c.Passing != nil {
				g := RoundGrade(*avg)
				row.Grade = strconv.Itoa(g)
				switch {
				case g >= *c.Passing:
					row.Situation = "Aprobado"
				case c.Provisional:
					row.Situation, row.Failed = "En riesgo", true
				default:
					row.Situation, row.Failed = "Desaprobado", true
				}
			}
		}
		d.Subjects = append(d.Subjects, row)
	}
}

// ---------- acta ----------

// Record is the PDF of an issued acta, from the content frozen when it was issued
func (s *DocumentService) Record(ctx context.Context, token, slug, id string) (PdfFile, error) {
	rec, err := s.closing.Record(ctx, token, slug, id)
	if err != nil {
		return PdfFile{}, err
	}
	var c RecordContent
	if err := json.Unmarshal(rec.Content, &c); err != nil {
		return PdfFile{}, fmt.Errorf("acta %s: %w", rec.Code, err)
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
		doc.Rows = append(doc.Rows, port.RecordRowDoc{N: r.N, Code: r.Code, Name: r.Name, Note: r.Note, Cells: r.Cells, Situation: r.Situation})
	}
	for _, sm := range c.Summary {
		doc.Summary = append(doc.Summary, fmt.Sprintf("%s: %d", sm.Label, sm.Count))
	}
	content, err := s.render.Record(doc)
	if err != nil {
		return PdfFile{}, err
	}
	return PdfFile{Name: fmt.Sprintf("acta-%s.pdf", slugify(rec.Code)), Content: content}, nil
}

func nonBlank(values ...string) []string {
	var out []string
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// ---------- receipt ----------

var methodNames = map[string]string{"cash": "Efectivo", "transfer": "Transferencia", "yape": "Yape", "plin": "Plin", "card": "Tarjeta", "deposit": "Depósito"}

// Receipt is the PDF of an internal payment receipt
func (s *DocumentService) Receipt(ctx context.Context, token, slug, id string) (PdfFile, error) {
	p, err := s.finance.Payment(ctx, token, slug, id)
	if err != nil {
		return PdfFile{}, err
	}
	method := methodNames[p.Method]
	if p.Reference != "" {
		method += " · operación " + p.Reference
	}
	doc := port.ReceiptDoc{Institution: s.finance.InstitutionName(ctx, token, slug), Number: p.Receipt, PaidOn: shortDate(p.PaidOn),
		Student: fmt.Sprintf("%s (%s)", p.StudentName, p.StudentCode), Payer: p.Payer, Method: method, Total: FormatSoles(p.Amount),
		ReceivedBy: p.ReceivedName, Annulled: p.Status == "annulled", AnnulReason: p.AnnulReason}
	// The logo is a nicety: a role that cannot read the academic profile still gets its receipt
	if actx, err := s.assessment.academic.Context(ctx, token, slug); err == nil {
		doc.Logo = imageOf(actx.Settings.Logo)
	}
	for _, a := range p.Allocations {
		doc.Lines = append(doc.Lines, port.ReceiptLineDoc{Concept: a.ChargeName, Amount: FormatSoles(a.Amount)})
	}
	content, err := s.render.Receipt(doc)
	if err != nil {
		return PdfFile{}, err
	}
	return PdfFile{Name: fmt.Sprintf("recibo-%s.pdf", slugify(p.Receipt)), Content: content}, nil
}
