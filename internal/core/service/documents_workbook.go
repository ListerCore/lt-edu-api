package service

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// SchedulesWorkbook is the period's timetable as an Excel file: every class in a filterable list, the week of each
// section and of each teacher in the course colours, and the legend of codes
func (s *DocumentService) SchedulesWorkbook(ctx context.Context, token, slug, periodID string) (PdfFile, error) {
	if periodID == "" {
		return PdfFile{}, domain.Validation(map[string]string{"periodId": "Elige el periodo."})
	}
	pl, err := s.teaching.Planner(ctx, token, slug, periodID)
	if err != nil {
		return PdfFile{}, err
	}
	if len(pl.Entries) == 0 {
		return PdfFile{}, domain.ErrNotFound
	}
	l := buildScheduleLegend(pl)
	days := pl.Context.Settings.WeekDays
	if len(days) == 0 {
		days = []int{1, 2, 3, 4, 5}
	}
	bySection := map[string]domain.Section{}
	for _, sec := range pl.Sections {
		bySection[sec.ID] = sec
	}
	text := func(v string) port.SheetCell { return port.SheetCell{Text: v} }

	// Every class, one per row, in section and time order
	entries := append([]domain.ScheduleEntry(nil), pl.Entries...)
	order := map[string]int{}
	for i, sec := range pl.Sections {
		order[sec.ID] = i
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if order[a.SectionID] != order[b.SectionID] {
			return order[a.SectionID] < order[b.SectionID]
		}
		if a.Weekday != b.Weekday {
			return a.Weekday < b.Weekday
		}
		return a.StartsAt < b.StartsAt
	})
	list := port.SheetTable{Columns: []string{"Nivel", "Grado", "Sección", "Turno", "Día", "Inicio", "Fin", "Sigla", "Curso", "Docente", "Aula"}}
	for _, e := range entries {
		sec := bySection[e.SectionID]
		c := l.cell(e, "")
		teacher := l.teachers[e.StaffID].name
		if teacher == "" {
			teacher = e.StaffName
		}
		list.Rows = append(list.Rows, []port.SheetCell{text(sec.LevelName), text(sec.GradeName), text(sec.Name), text(shiftNames[e.Shift]), text(weekdayNames[e.Weekday]),
			text(e.StartsAt), text(e.EndsAt), {Text: c.Code, Fill: c.Fill, Bold: true}, text(e.SubjectName), text(orDefault(teacher, "Sin asignar")), text(e.Room)})
	}

	// A week as a table: hours down, days across
	week := func(title string, blocks []domain.TimeBlock, mine []domain.ScheduleEntry, detail func(domain.ScheduleEntry) string) port.SheetTable {
		t := port.SheetTable{Title: title, Columns: []string{"Hora"}}
		for _, d := range days {
			t.Columns = append(t.Columns, weekdayNames[d])
		}
		at := map[string]domain.ScheduleEntry{}
		for _, e := range mine {
			at[fmt.Sprintf("%d|%s", e.Weekday, e.StartsAt)] = e
		}
		for _, b := range blocks {
			row := []port.SheetCell{text(b.StartsAt + "–" + b.EndsAt)}
			for _, d := range days {
				switch e, ok := at[fmt.Sprintf("%d|%s", d, b.StartsAt)]; {
				case b.Kind == "break":
					row = append(row, port.SheetCell{Text: "Recreo", Fill: &port.Colour{R: 243, G: 243, B: 243}})
				case ok:
					c := l.cell(e, "")
					row = append(row, port.SheetCell{Text: c.Code + "\n" + detail(e), Fill: c.Fill})
				default:
					row = append(row, port.SheetCell{})
				}
			}
			t.Rows = append(t.Rows, row)
		}
		return t
	}
	widths := []float64{13}
	for range days {
		widths = append(widths, 18)
	}
	sections := port.Sheet{Name: "Por sección", Widths: widths}
	for _, sec := range pl.Sections {
		var mine []domain.ScheduleEntry
		for _, e := range pl.Entries {
			if e.SectionID == sec.ID {
				mine = append(mine, e)
			}
		}
		if len(mine) == 0 {
			continue
		}
		title := fmt.Sprintf("%s · turno %s · tutor(a): %s", sectionTitle(sec, true), shiftNames[sec.Shift], orDefault(sec.TutorName, "por asignar"))
		sections.Tables = append(sections.Tables, week(title, blocksOf(pl.Blocks, sec.Shift), mine, func(e domain.ScheduleEntry) string { return l.teachers[e.StaffID].short }))
	}
	teachers := port.Sheet{Name: "Por docente", Widths: widths}
	ids := make([]string, 0, len(l.teachers))
	for id := range l.teachers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return fold(l.teachers[ids[i]].name) < fold(l.teachers[ids[j]].name) })
	hours := map[string]int{}
	for _, id := range ids {
		var mine []domain.ScheduleEntry
		var shifts []string
		for _, e := range pl.Entries {
			if e.StaffID == id {
				mine = append(mine, e)
				if !slices.Contains(shifts, e.Shift) {
					shifts = append(shifts, e.Shift)
				}
			}
		}
		hours[id] = len(mine)
		teachers.Tables = append(teachers.Tables, week(fmt.Sprintf("%s · %d horas semanales", l.teachers[id].name, len(mine)), blocksOf(pl.Blocks, shifts...), mine, func(e domain.ScheduleEntry) string {
			if sec, ok := bySection[e.SectionID]; ok {
				return sectionTitle(sec, true)
			}
			return e.SectionLabel
		}))
	}

	courses := port.SheetTable{Title: "Cursos", Columns: []string{"Sigla", "Curso", "Horas semanales"}}
	for _, n := range l.order {
		c := l.courses[n]
		fill := c.fill
		count := 0
		for _, e := range pl.Entries {
			if e.SubjectName == n {
				count++
			}
		}
		courses.Rows = append(courses.Rows, []port.SheetCell{{Text: c.code, Fill: &fill, Bold: true}, text(n), text(strconv.Itoa(count))})
	}
	people := port.SheetTable{Title: "Docentes", Columns: []string{"Sigla", "Docente", "Horas semanales"}}
	for _, id := range ids {
		t := l.teachers[id]
		people.Rows = append(people.Rows, []port.SheetCell{{Text: t.code, Bold: true}, text(t.name), text(strconv.Itoa(hours[id]))})
	}

	book := port.Workbook{Title: "Horarios " + pl.Period.Name, Sheets: []port.Sheet{
		{Name: "Clases", Widths: []float64{14, 16, 10, 10, 11, 8, 8, 8, 34, 32, 12}, Tables: []port.SheetTable{list}, Filter: true},
		sections, teachers,
		{Name: "Leyenda", Widths: []float64{10, 44, 16}, Tables: []port.SheetTable{courses, people}},
	}}
	content, err := s.sheets.Workbook(book)
	if err != nil {
		return PdfFile{}, err
	}
	return PdfFile{Name: fmt.Sprintf("horarios-%s.xlsx", slugify(pl.Period.Name)), Content: content}, nil
}
