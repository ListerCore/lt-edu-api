package service

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// What a published timetable groups by
const (
	ScheduleBySection = "section"
	ScheduleByGrade   = "grade"
	ScheduleByTeacher = "teacher"
	ScheduleGeneral   = "general"
)

var (
	weekdayNames = map[int]string{1: "Lunes", 2: "Martes", 3: "Miércoles", 4: "Jueves", 5: "Viernes", 6: "Sábado", 7: "Domingo"}
	weekdayShort = map[int]string{1: "Lun", 2: "Mar", 3: "Mié", 4: "Jue", 5: "Vie", 6: "Sáb", 7: "Dom"}
	shiftNames   = map[string]string{"morning": "mañana", "afternoon": "tarde", "evening": "noche"}
	// Words left out of a course's initials ("Ciencia y Tecnología" → CT, "Educación para el Trabajo" → EPT)
	minorWords = map[string]bool{"y": true, "e": true, "de": true, "del": true, "la": true, "las": true, "el": true, "los": true, "en": true, "para": true, "como": true, "a": true, "con": true}
	// Soft fills that print well in colour and stay legible in greyscale, in a fixed order by course name
	courseFills = []port.Colour{{R: 253, G: 226, B: 207}, {R: 214, G: 230, B: 251}, {R: 211, G: 240, B: 225}, {R: 251, G: 239, B: 200}, {R: 246, G: 216, B: 230}, {R: 226, G: 220, B: 247}, {R: 217, G: 238, B: 243}, {R: 239, G: 227, B: 211}, {R: 229, G: 240, B: 207}, {R: 243, G: 217, B: 211}, {R: 223, G: 227, B: 232}, {R: 240, G: 230, B: 246}, {R: 255, G: 243, B: 184}, {R: 216, G: 243, B: 236}}
)

// fold drops accents and case, to sort Spanish names as people expect
func fold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// initials is a course's short code: the initials of its significant words, or the first three letters of one word
func initials(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || r == ',' || r == '.' || r == '(' || r == ')' })
	var keep []string
	for _, w := range words {
		if !minorWords[strings.ToLower(w)] {
			keep = append(keep, w)
		}
	}
	if len(keep) == 1 {
		r := []rune(keep[0])
		return strings.ToUpper(string(r[:min(3, len(r))]))
	}
	var b strings.Builder
	for _, w := range keep {
		b.WriteString(strings.ToUpper(string([]rune(w)[0])))
	}
	return b.String()
}

// uniqueCodes gives each name its own code; a repeated one takes a number ("CS", "CS2")
func uniqueCodes(names []string, codeOf func(string) string) map[string]string {
	out := map[string]string{}
	used := map[string]bool{}
	for _, n := range names {
		code := codeOf(n)
		base := code
		for i := 2; used[code]; i++ {
			code = base + strconv.Itoa(i)
		}
		used[code] = true
		out[n] = code
	}
	return out
}

type courseLook struct {
	code string
	fill port.Colour
}

type teacherLook struct {
	code, name, short string
}

// scheduleLegend holds the codes and colours of a whole period, so a course looks the same on every page
type scheduleLegend struct {
	courses  map[string]courseLook
	order    []string
	teachers map[string]teacherLook
}

func buildScheduleLegend(pl Planner) scheduleLegend {
	seen := map[string]bool{}
	var names []string
	for _, e := range pl.Entries {
		if !seen[e.SubjectName] {
			seen[e.SubjectName] = true
			names = append(names, e.SubjectName)
		}
	}
	sort.Slice(names, func(i, j int) bool { return fold(names[i]) < fold(names[j]) })
	codes := uniqueCodes(names, initials)
	l := scheduleLegend{courses: map[string]courseLook{}, order: names, teachers: map[string]teacherLook{}}
	for i, n := range names {
		l.courses[n] = courseLook{code: codes[n], fill: courseFills[i%len(courseFills)]}
	}
	staff := map[string]domain.Staff{}
	for _, t := range pl.Teachers {
		staff[t.ID] = t
	}
	byName := map[string]string{}
	var people []string
	for _, e := range pl.Entries {
		if e.StaffID == "" || byName[e.StaffID] != "" {
			continue
		}
		name := e.StaffName
		if t, ok := staff[e.StaffID]; ok {
			name = t.GivenNames + " " + t.Surnames
		}
		byName[e.StaffID] = name
		people = append(people, name)
	}
	sort.Slice(people, func(i, j int) bool { return fold(people[i]) < fold(people[j]) })
	tcodes := uniqueCodes(people, func(n string) string {
		var b strings.Builder
		for _, w := range strings.Fields(n) {
			if b.Len() < 3 {
				b.WriteString(strings.ToUpper(string([]rune(w)[0])))
			}
		}
		return b.String()
	})
	for id, name := range byName {
		short := name
		if t, ok := staff[id]; ok && t.GivenNames != "" {
			short = string([]rune(t.GivenNames)[0]) + ". " + strings.Fields(t.Surnames + " ")[0]
		}
		l.teachers[id] = teacherLook{code: tcodes[name], name: name, short: short}
	}
	return l
}

func (l scheduleLegend) cell(e domain.ScheduleEntry, detail string) port.GridCell {
	c, ok := l.courses[e.SubjectName]
	if !ok {
		return port.GridCell{Code: e.SubjectName, Detail: detail}
	}
	fill := c.fill
	return port.GridCell{Code: c.code, Detail: detail, Fill: &fill}
}

// sectionTitle: "4.° A" (with the level when asked)
func sectionTitle(s domain.Section, level bool) string {
	t := strings.TrimSpace(s.GradeShort + " " + s.Name)
	if level {
		t += " · " + s.LevelName
	}
	return t
}

// blocksOf are the hours of some shifts in time order
func blocksOf(blocks []domain.TimeBlock, shifts ...string) []domain.TimeBlock {
	var out []domain.TimeBlock
	for _, b := range blocks {
		if slices.Contains(shifts, b.Shift) {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartsAt < out[j].StartsAt })
	return out
}

// weekGrid is a week: hours down, days across; each class shows its course code and a detail
func weekGrid(caption string, days []int, short bool, blocks []domain.TimeBlock, entries []domain.ScheduleEntry, l scheduleLegend, detail func(domain.ScheduleEntry) string) port.ScheduleGrid {
	g := port.ScheduleGrid{Caption: caption, Corner: "Hora"}
	for _, d := range days {
		name := weekdayNames[d]
		if short {
			name = weekdayShort[d]
		}
		g.Columns = append(g.Columns, strings.ToUpper(name))
	}
	at := map[string]domain.ScheduleEntry{}
	for _, e := range entries {
		at[fmt.Sprintf("%d|%s", e.Weekday, e.StartsAt)] = e
	}
	for _, b := range blocks {
		if b.Kind == "break" {
			g.Rows = append(g.Rows, port.GridRow{Head: b.StartsAt, Break: true})
			continue
		}
		row := port.GridRow{Head: b.StartsAt + "\n" + b.EndsAt}
		for _, d := range days {
			if e, ok := at[fmt.Sprintf("%d|%s", d, b.StartsAt)]; ok {
				row.Cells = append(row.Cells, l.cell(e, detail(e)))
			} else {
				row.Cells = append(row.Cells, port.GridCell{})
			}
		}
		g.Rows = append(g.Rows, row)
	}
	return g
}

// courseLegend lists the courses of some classes: code, course, who (teachers or sections) and weekly hours
func courseLegend(entries []domain.ScheduleEntry, l scheduleLegend, whoLabel string, who func([]domain.ScheduleEntry) string) port.LegendTable {
	by := map[string][]domain.ScheduleEntry{}
	for _, e := range entries {
		by[e.SubjectName] = append(by[e.SubjectName], e)
	}
	t := port.LegendTable{Title: "LEYENDA", Columns: []string{"Sigla", "Curso", whoLabel, "Horas"}, Widths: []float64{12, 34, 44, 10}}
	names := make([]string, 0, len(by))
	for n := range by {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return l.courses[names[i]].code < l.courses[names[j]].code })
	for _, n := range names {
		c := l.courses[n]
		fill := c.fill
		t.Rows = append(t.Rows, port.LegendRow{Fills: []*port.Colour{&fill}, Cells: []string{c.code, n, who(by[n]), strconv.Itoa(len(by[n]))}})
	}
	return t
}

// teacherNames of some classes, without repeats
func (l scheduleLegend) teacherNames(list []domain.ScheduleEntry) string {
	var out []string
	for _, e := range list {
		name := l.teachers[e.StaffID].name
		if name == "" {
			name = e.StaffName
		}
		if name != "" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return "Sin asignar"
	}
	return strings.Join(out, ", ")
}

// scheduleBuild is a set of timetable sheets ready to print or to freeze as a version
type scheduleBuild struct {
	doc port.ScheduleDoc
	// Download name without extension, and the title the publication is followed by
	file, title string
	period      domain.Period
}

// buildSchedules lays out the timetables to publish: by section, by grade, by teacher (one sheet each, or only the
// target) or the general one (a sheet per shift, every section under each day). Legends always go below the grids
func (s *DocumentService) buildSchedules(ctx context.Context, token, slug, periodID, mode, target string) (scheduleBuild, error) {
	if !slices.Contains([]string{ScheduleBySection, ScheduleByGrade, ScheduleByTeacher, ScheduleGeneral}, mode) {
		return scheduleBuild{}, domain.Validation(map[string]string{"mode": "Elige por sección, por grado, por docente o general."})
	}
	if periodID == "" {
		return scheduleBuild{}, domain.Validation(map[string]string{"periodId": "Elige el periodo."})
	}
	pl, err := s.teaching.Planner(ctx, token, slug, periodID)
	if err != nil {
		return scheduleBuild{}, err
	}
	l := buildScheduleLegend(pl)
	days := pl.Context.Settings.WeekDays
	if len(days) == 0 {
		days = []int{1, 2, 3, 4, 5}
	}
	title := strings.ToUpper(pl.Period.Name)
	// Only sections with classes placed are published (inicial plans, for one, carry no hours)
	var sections []domain.Section
	for _, sec := range pl.Sections {
		if slices.ContainsFunc(pl.Entries, func(e domain.ScheduleEntry) bool { return e.SectionID == sec.ID }) {
			sections = append(sections, sec)
		}
	}
	of := func(pred func(domain.ScheduleEntry) bool) []domain.ScheduleEntry {
		var out []domain.ScheduleEntry
		for _, e := range pl.Entries {
			if pred(e) {
				out = append(out, e)
			}
		}
		return out
	}
	shortOf := func(e domain.ScheduleEntry) string { return l.teachers[e.StaffID].short }
	b := scheduleBuild{doc: port.ScheduleDoc{Institution: pl.Context.Institution.Name, Logo: imageOf(pl.Context.Settings.Logo), IssuedOn: s.assessment.attendance.today()}, period: pl.Period}
	name, what := "horarios", ""
	switch mode {
	case ScheduleBySection:
		what = "Horarios por sección"
		for _, sec := range sections {
			if target != "" && sec.ID != target {
				continue
			}
			entries := of(func(e domain.ScheduleEntry) bool { return e.SectionID == sec.ID })
			sub := sec.Name + " · " + sec.LevelName
			if sec.GradeName != "" {
				sub = fmt.Sprintf("%s «%s» · %s", sec.GradeName, sec.Name, sec.LevelName)
			}
			info := fmt.Sprintf("Turno %s · Tutor(a): %s", shiftNames[sec.Shift], orDefault(sec.TutorName, "por asignar"))
			if sec.Room != "" {
				info += " · Aula: " + sec.Room
			}
			info += fmt.Sprintf(" · %d horas semanales", len(entries))
			b.doc.Pages = append(b.doc.Pages, port.SchedulePage{Title: "HORARIO DE CLASES " + title, Subtitle: sub, Info: info,
				Grids:   []port.ScheduleGrid{weekGrid("", days, false, blocksOf(pl.Blocks, sec.Shift), entries, l, shortOf)},
				Legends: []port.LegendTable{courseLegend(entries, l, "Docente", l.teacherNames)}})
			if target != "" {
				name, what = "horario-"+slugify(sectionTitle(sec, true)), "Horario de "+sectionTitle(sec, true)
			}
		}
	case ScheduleByGrade:
		what = "Horarios por grado"
		for _, g := range gradesOfSections(sections) {
			if target != "" && g.id != target {
				continue
			}
			ids := map[string]bool{}
			for _, sec := range g.sections {
				ids[sec.ID] = true
			}
			entries := of(func(e domain.ScheduleEntry) bool { return ids[e.SectionID] })
			page := port.SchedulePage{Title: "HORARIO DE CLASES " + title, Subtitle: g.name}
			for _, sec := range g.sections {
				caption := fmt.Sprintf("Sección %s · %s · tutor(a): %s", sec.Name, shiftNames[sec.Shift], orDefault(sec.TutorName, "por asignar"))
				page.Grids = append(page.Grids, weekGrid(caption, days, len(g.sections) > 1, blocksOf(pl.Blocks, sec.Shift), of(func(e domain.ScheduleEntry) bool { return e.SectionID == sec.ID }), l, shortOf))
			}
			page.Legends = []port.LegendTable{courseLegend(entries, l, "Docente", func(list []domain.ScheduleEntry) string {
				// One name when the same teacher has the course in every section; otherwise section by section
				var parts []string
				names := map[string]bool{}
				for _, sec := range g.sections {
					var mine []domain.ScheduleEntry
					for _, e := range list {
						if e.SectionID == sec.ID {
							mine = append(mine, e)
						}
					}
					if len(mine) == 0 {
						continue
					}
					n := l.teacherNames(mine)
					names[n] = true
					parts = append(parts, sec.Name+": "+n)
				}
				if len(names) == 1 {
					for n := range names {
						return n
					}
				}
				return strings.Join(parts, " · ")
			})}
			b.doc.Pages = append(b.doc.Pages, page)
			if target != "" {
				name, what = "horario-"+slugify(g.name), "Horario de "+g.name
			}
		}
	case ScheduleByTeacher:
		what = "Horarios por docente"
		ids := make([]string, 0, len(l.teachers))
		for id := range l.teachers {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return fold(l.teachers[ids[i]].name) < fold(l.teachers[ids[j]].name) })
		bySection := map[string]domain.Section{}
		for _, sec := range pl.Sections {
			bySection[sec.ID] = sec
		}
		for _, id := range ids {
			if target != "" && id != target {
				continue
			}
			entries := of(func(e domain.ScheduleEntry) bool { return e.StaffID == id })
			var shifts []string
			secs := map[string]bool{}
			for _, e := range entries {
				if !slices.Contains(shifts, e.Shift) {
					shifts = append(shifts, e.Shift)
				}
				secs[e.SectionID] = true
			}
			t := l.teachers[id]
			b.doc.Pages = append(b.doc.Pages, port.SchedulePage{Title: "HORARIO DEL DOCENTE " + title, Subtitle: t.name,
				Info:  fmt.Sprintf("%d horas semanales · %d secciones", len(entries), len(secs)),
				Grids: []port.ScheduleGrid{teacherGrid(days, blocksOf(pl.Blocks, shifts...), entries, l, bySection)},
				Legends: []port.LegendTable{courseLegend(entries, l, "Secciones", func(list []domain.ScheduleEntry) string {
					// Sections grouped by level, since "1.° A" exists in primaria and in secundaria
					var levels []string
					byLevel := map[string][]string{}
					for _, sec := range pl.Sections {
						if slices.ContainsFunc(list, func(e domain.ScheduleEntry) bool { return e.SectionID == sec.ID }) {
							if byLevel[sec.LevelName] == nil {
								levels = append(levels, sec.LevelName)
							}
							byLevel[sec.LevelName] = append(byLevel[sec.LevelName], sectionTitle(sec, false))
						}
					}
					parts := make([]string, len(levels))
					for i, lv := range levels {
						parts[i] = lv + ": " + strings.Join(byLevel[lv], ", ")
					}
					return strings.Join(parts, " · ")
				})}})
			if target != "" {
				name, what = "horario-"+slugify(t.name), "Horario de "+t.name
			}
		}
	case ScheduleGeneral:
		b.doc.Pages = generalPages(pl, sections, days, title, l)
		name, what = "horario-general", "Horario general"
	}
	if len(b.doc.Pages) == 0 {
		return b, domain.ErrNotFound
	}
	b.file = fmt.Sprintf("%s-%s", name, slugify(pl.Period.Name))
	b.title = what + " · " + pl.Period.Name
	return b, nil
}

// teacherGrid is a teacher's week: hours down and, under each day, the course and the section of each class
func teacherGrid(days []int, blocks []domain.TimeBlock, entries []domain.ScheduleEntry, l scheduleLegend, bySection map[string]domain.Section) port.ScheduleGrid {
	g := port.ScheduleGrid{Corner: "Hora", SubColumns: []string{"Curso", "Sección"}}
	for _, d := range days {
		g.Columns = append(g.Columns, strings.ToUpper(weekdayNames[d]))
	}
	at := map[string]domain.ScheduleEntry{}
	for _, e := range entries {
		at[fmt.Sprintf("%d|%s", e.Weekday, e.StartsAt)] = e
	}
	for _, bl := range blocks {
		if bl.Kind == "break" {
			g.Rows = append(g.Rows, port.GridRow{Head: bl.StartsAt, Break: true})
			continue
		}
		row := port.GridRow{Head: bl.StartsAt + "\n" + bl.EndsAt}
		for _, d := range days {
			e, ok := at[fmt.Sprintf("%d|%s", d, bl.StartsAt)]
			if !ok {
				row.Cells = append(row.Cells, port.GridCell{}, port.GridCell{})
				continue
			}
			course := l.cell(e, "")
			place := port.GridCell{Code: e.SectionLabel, Fill: course.Fill}
			if sec, ok := bySection[e.SectionID]; ok {
				place.Code, place.Detail = sectionTitle(sec, false), sec.LevelName
			}
			row.Cells = append(row.Cells, course, place)
		}
		g.Rows = append(g.Rows, row)
	}
	return g
}

// chipTable lays code/name pairs a few per line, each code with its colour chip when it has one
func chipTable(title, head string, codes, names []string, fills []*port.Colour, perLine int) port.LegendTable {
	t := port.LegendTable{Title: title}
	// Equal pairs: a short code column and a wide name column
	for range perLine {
		t.Columns = append(t.Columns, "Sigla", head)
		t.Widths = append(t.Widths, 6, 100/float64(perLine)-6)
	}
	var row port.LegendRow
	for i := range codes {
		row.Cells = append(row.Cells, codes[i], names[i])
		row.Fills = append(row.Fills, fills[i], nil)
		if len(row.Cells) == 2*perLine || i == len(codes)-1 {
			for len(row.Cells) < 2*perLine {
				row.Cells = append(row.Cells, "")
				row.Fills = append(row.Fills, nil)
			}
			t.Rows = append(t.Rows, row)
			row = port.LegendRow{}
		}
	}
	return t
}

// maxGeneralColumns caps the narrow columns of a general sheet (days × sections) so codes stay legible on A4
const maxGeneralColumns = 40

// generalPages: for each shift, the class hours down and, under each day, a narrow column per section with the
// course and teacher codes. Sections go by level, and a level with too many sections for one sheet takes several;
// each sheet carries the legend of the courses and teachers it shows
func generalPages(pl Planner, sections []domain.Section, days []int, title string, l scheduleLegend) []port.SchedulePage {
	at := map[string]domain.ScheduleEntry{}
	for _, e := range pl.Entries {
		at[fmt.Sprintf("%s|%d|%s", e.SectionID, e.Weekday, e.StartsAt)] = e
	}
	perSheet := max(1, maxGeneralColumns/len(days))
	var pages []port.SchedulePage
	var shifts []string
	for _, sec := range sections {
		if !slices.Contains(shifts, sec.Shift) {
			shifts = append(shifts, sec.Shift)
		}
	}
	for _, shift := range shifts {
		// Sections of the shift by level, in their order
		var levels []string
		byLevel := map[string][]domain.Section{}
		for _, sec := range sections {
			if sec.Shift != shift {
				continue
			}
			if byLevel[sec.LevelName] == nil {
				levels = append(levels, sec.LevelName)
			}
			byLevel[sec.LevelName] = append(byLevel[sec.LevelName], sec)
		}
		// Small levels share a sheet when they fit together
		var groups [][]domain.Section
		var names []string
		for _, lv := range levels {
			list := byLevel[lv]
			if n := len(groups); n > 0 && len(groups[n-1])+len(list) <= perSheet {
				groups[n-1] = append(groups[n-1], list...)
				names[n-1] += " · " + lv
				continue
			}
			// A big level splits in even sheets (12 sections: 6 and 6, not 8 and 4)
			sheets := (len(list) + perSheet - 1) / perSheet
			size := (len(list) + sheets - 1) / sheets
			for i := 0; i < len(list); i += size {
				groups = append(groups, list[i:min(i+size, len(list))])
				label := lv
				if sheets > 1 {
					label = fmt.Sprintf("%s (%d/%d)", lv, i/size+1, sheets)
				}
				names = append(names, label)
			}
		}
		blocks := blocksOf(pl.Blocks, shift)
		for gi, group := range groups {
			g := port.ScheduleGrid{Corner: "Hora"}
			for _, d := range days {
				g.Columns = append(g.Columns, strings.ToUpper(weekdayNames[d]))
			}
			for _, sec := range group {
				g.SubColumns = append(g.SubColumns, sectionTitle(sec, false))
			}
			courses, teachers := map[string]bool{}, map[string]bool{}
			for _, bl := range blocks {
				if bl.Kind == "break" {
					g.Rows = append(g.Rows, port.GridRow{Head: bl.StartsAt, Break: true})
					continue
				}
				r := port.GridRow{Head: bl.StartsAt + "\n" + bl.EndsAt}
				for _, d := range days {
					for _, sec := range group {
						e, ok := at[fmt.Sprintf("%s|%d|%s", sec.ID, d, bl.StartsAt)]
						if !ok {
							r.Cells = append(r.Cells, port.GridCell{})
							continue
						}
						courses[e.SubjectName] = true
						if e.StaffID != "" {
							teachers[e.StaffID] = true
						}
						r.Cells = append(r.Cells, l.cell(e, l.teachers[e.StaffID].code))
					}
				}
				g.Rows = append(g.Rows, r)
			}
			var codes, labels []string
			var fills []*port.Colour
			for _, n := range l.order {
				if courses[n] {
					fill := l.courses[n].fill
					codes, labels, fills = append(codes, l.courses[n].code), append(labels, n), append(fills, &fill)
				}
			}
			legends := []port.LegendTable{chipTable("CURSOS", "Curso", codes, labels, fills, 4)}
			var ts []teacherLook
			for id := range teachers {
				ts = append(ts, l.teachers[id])
			}
			sort.Slice(ts, func(i, j int) bool { return ts[i].code < ts[j].code })
			codes, labels, fills = nil, nil, nil
			for _, t := range ts {
				codes, labels, fills = append(codes, t.code), append(labels, t.name), append(fills, nil)
			}
			if len(ts) > 0 {
				legends = append(legends, chipTable("DOCENTES", "Docente", codes, labels, fills, 4))
			}
			pages = append(pages, port.SchedulePage{Title: "HORARIO GENERAL " + title, Subtitle: strings.ToUpper("turno " + shiftNames[shift] + " · " + names[gi]),
				Info: fmt.Sprintf("%d secciones · en cada celda, la sigla del curso y la del docente", len(group)), Grids: []port.ScheduleGrid{g}, Legends: legends})
		}
	}
	return pages
}

type gradeGroup struct {
	id, name string
	sections []domain.Section
}

// gradesOfSections groups sections by grade, in their order (academies' areas without grade stand alone)
func gradesOfSections(sections []domain.Section) []gradeGroup {
	var out []gradeGroup
	index := map[string]int{}
	for _, sec := range sections {
		key := sec.GradeID
		name := sec.GradeName + " · " + sec.LevelName
		if key == "" {
			key, name = "section:"+sec.ID, sec.Name+" · "+sec.LevelName
		}
		i, ok := index[key]
		if !ok {
			i = len(out)
			index[key] = i
			out = append(out, gradeGroup{id: key, name: name})
		}
		out[i].sections = append(out[i].sections, sec)
	}
	return out
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
