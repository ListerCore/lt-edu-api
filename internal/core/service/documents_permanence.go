package service

import (
	"context"
	"fmt"
	"strconv"

	"api/internal/core/port"
)

// Permanence report of a distance section (phase E5): the summary PDF (weeks present, percent, activity and last
// action per student) and the spreadsheet with every week

// SetParticipation gives the service the permanence reports
func (s *DocumentService) SetParticipation(p *ParticipationService) { s.participation = p }

func lastAction(r StudentPermanence) string {
	if r.Activity.LastAt == nil {
		return "Sin actividad"
	}
	return shortDate(r.Activity.LastAt.In(lima()))
}

func (s *DocumentService) PermanencePdf(ctx context.Context, token, slug, sectionID string) (PdfFile, error) {
	sc, err := s.participation.scope(ctx, token, slug)
	if err != nil {
		return PdfFile{}, err
	}
	rep, err := s.participation.section(ctx, sc, sectionID)
	if err != nil {
		return PdfFile{}, err
	}
	h := port.DocHeader{Institution: sc.institution.Name, Title: "Registro de asistencia (permanencia)", Subtitle: rep.PeriodName}
	if actx, err := s.assessment.academic.Context(ctx, token, slug); err == nil {
		h.Dre, h.Ugel, h.ModularCode, h.Logo = actx.Settings.Dre, actx.Settings.Ugel, actx.Settings.ModularCode, imageOf(actx.Settings.Logo)
	}
	g := port.TableGroup{Title: rep.Label, Info: []string{
		fmt.Sprintf("Regla: al menos %d día(s) con actividad cada %d días", rep.Rule.MinActiveDays, rep.Rule.WindowDays),
		"Del " + shortDate(rep.From) + " al " + shortDate(rep.To)}}
	for i, r := range rep.Students {
		status := ""
		if r.Student.Status != "active" {
			status = " (" + map[string]string{"withdrawn": "retirado", "transferred": "trasladado", "completed": "concluyó"}[r.Student.Status] + ")"
		}
		adjusted := 0
		for _, w := range r.Windows {
			if w.Adjusted {
				adjusted++
			}
		}
		g.Rows = append(g.Rows, []string{strconv.Itoa(i + 1), r.Student.StudentCode, r.Student.StudentName + status,
			fmt.Sprintf("%d de %d", r.Present, r.Counted), strconv.Itoa(r.Percent) + " %", strconv.Itoa(len(r.Activity.Days)),
			strconv.Itoa(r.Activity.Views + r.Activity.Live), strconv.Itoa(r.Activity.Submissions), strconv.Itoa(r.Activity.Posts), lastAction(r), strconv.Itoa(adjusted)})
	}
	g.Total = fmt.Sprintf("Total: %d estudiantes", len(rep.Students))
	doc := port.TableDoc{Header: h, IssuedOn: s.now().In(lima()), Groups: []port.TableGroup{g}, Signatures: []string{"Tutor(a)", "Dirección"},
		Columns: []string{"N.°", "Código", "Estudiante", "Semanas con actividad", "Permanencia", "Días activos", "Accesos a materiales", "Entregas", "Foros", "Última actividad", "Ajustes"},
		Widths:  []float64{4, 8, 30, 10, 8, 7, 8, 7, 6, 10, 6},
		Legend:  "Permanencia en la forma a distancia según la regla del CEBA. La semana en curso solo suma cuando ya cumple la regla. Los ajustes son del tutor, con justificación."}
	content, err := s.render.Table(doc)
	if err != nil {
		return PdfFile{}, err
	}
	return PdfFile{Name: "permanencia-" + slugify(rep.Label) + ".pdf", Content: content}, nil
}

func (s *DocumentService) PermanenceXlsx(ctx context.Context, token, slug, sectionID string) (PdfFile, error) {
	sc, err := s.participation.scope(ctx, token, slug)
	if err != nil {
		return PdfFile{}, err
	}
	rep, err := s.participation.section(ctx, sc, sectionID)
	if err != nil {
		return PdfFile{}, err
	}
	present, absent, adjusted := &port.Colour{R: 214, G: 240, B: 220}, &port.Colour{R: 250, G: 222, B: 222}, &port.Colour{R: 255, G: 236, B: 200}
	weeks := 0
	for _, r := range rep.Students {
		weeks = max(weeks, len(r.Windows))
	}
	cols := []string{"N.°", "Código", "Estudiante", "Permanencia %", "Días activos", "Última actividad"}
	widths := []float64{5, 12, 34, 13, 11, 14}
	// Weeks are numbered from the period's start; each student's first week is the one they enrolled in
	var first []string
	for _, r := range rep.Students {
		if len(r.Windows) == weeks {
			for _, w := range r.Windows {
				first = append(first, shortDate(w.Start)[:5])
			}
			break
		}
	}
	for i := 0; i < weeks; i++ {
		label := "S" + strconv.Itoa(i+1)
		if i < len(first) {
			label += " " + first[i]
		}
		cols = append(cols, label)
		widths = append(widths, 9)
	}
	rows := [][]port.SheetCell{}
	for i, r := range rep.Students {
		row := []port.SheetCell{{Text: strconv.Itoa(i + 1)}, {Text: r.Student.StudentCode}, {Text: r.Student.StudentName}, {Text: strconv.Itoa(r.Percent)},
			{Text: strconv.Itoa(len(r.Activity.Days))}, {Text: lastAction(r)}}
		for j := 0; j < weeks-len(r.Windows); j++ {
			row = append(row, port.SheetCell{Text: ""})
		}
		for _, w := range r.Windows {
			c := port.SheetCell{Text: strconv.Itoa(w.ActiveDays), Fill: absent}
			if w.Present {
				c.Fill = present
			}
			if w.Adjusted {
				c.Fill, c.Text = adjusted, c.Text+" (ajuste)"
			}
			row = append(row, c)
		}
		rows = append(rows, row)
	}
	book := port.Workbook{Title: "Permanencia " + rep.Label, Sheets: []port.Sheet{{Name: "Permanencia", Widths: widths, Filter: true,
		Tables: []port.SheetTable{{Title: sc.institution.Name + " · Permanencia " + rep.Label + " · " + rep.PeriodName + " (días con actividad por semana)", Columns: cols, Rows: rows}}}}}
	content, err := s.sheets.Workbook(book)
	if err != nil {
		return PdfFile{}, err
	}
	return PdfFile{Name: "permanencia-" + slugify(rep.Label) + ".xlsx", Content: content}, nil
}
