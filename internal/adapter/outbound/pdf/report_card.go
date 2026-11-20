package pdf

import (
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"

	"api/internal/core/port"
)

// A4 landscape: two faces side by side (front and back of the folded report)
const (
	pageW = 297.0
	pageH = 210.0
	gap   = 8.0
	faceW = (pageW - 2*margin - gap) / 2
	faceH = pageH - 2*margin
)

var shaded = &port.Colour{R: 189, G: 189, B: 189}

// What each level means, as printed on the SIAGIE report (Currículo Nacional de la Educación Básica)
var scaleRows = [][3]string{
	{"AD", "Logro destacado", "Cuando el estudiante evidencia un nivel superior a lo esperado respecto a la competencia. Esto quiere decir que demuestra aprendizajes que van más allá del nivel esperado."},
	{"A", "Logro esperado", "Cuando el estudiante evidencia el nivel esperado respecto a la competencia, demostrando manejo satisfactorio en todas las tareas propuestas y en el tiempo programado."},
	{"B", "En proceso", "Cuando el estudiante está próximo o cerca al nivel esperado respecto a la competencia, para lo cual requiere acompañamiento durante un tiempo razonable para lograrlo."},
	{"C", "En inicio", "Cuando el estudiante muestra un progreso mínimo en una competencia de acuerdo al nivel esperado. Evidencia con frecuencia dificultades en el desarrollo de las tareas, por lo que necesita mayor tiempo de acompañamiento e intervención del docente."},
}

// ReportCards draws one sheet per card: literal cards in the SIAGIE layout (A4 landscape, front and back), the
// others as a grades list (A4 portrait)
func (r *Renderer) ReportCards(cards []port.ReportCardDoc) ([]byte, error) {
	landscape := len(cards) == 0 || cards[0].Literal
	pdf := newDoc(landscape, "Informe de progreso")
	for _, c := range cards {
		if c.Literal {
			pdf.AddPageFormat("L", fpdf.SizeType{Wd: 210, Ht: 297})
			literalCard(pdf, c)
		} else {
			pdf.AddPageFormat("P", fpdf.SizeType{Wd: 210, Ht: 297})
			numericCard(pdf, c)
		}
	}
	if len(cards) == 0 {
		pdf.AddPage()
	}
	return output(pdf)
}

// gridWidths: area, competency, one per term, annual, area annual, final conclusion
func gridWidths(terms int) []float64 {
	rel := []float64{14, 0}
	for range terms {
		rel = append(rel, 4.5)
	}
	rel = append(rel, 6, 6, 19)
	fixed := 0.0
	for _, v := range rel {
		fixed += v
	}
	rel[1] = 100 - fixed
	return cols(faceW, rel...)
}

// gridHeader draws the two header rows of the competencies grid
func gridHeader(p *pen, x, y float64, terms int) float64 {
	w := gridWidths(terms)
	termsW := 0.0
	for i := range terms {
		termsW += w[2+i]
	}
	top := []box{{text: "ÁREA CURRICULAR", bold: true}, {text: "COMPETENCIAS", bold: true}, {text: "CALIFICATIVO POR PERIODO", bold: true}, {text: "Calif. anual de comp.", bold: true, scale: 0.8}, {text: "Calif. anual de área", bold: true, scale: 0.8}, {text: "Conclusión descriptiva de final del periodo lectivo", bold: true, scale: 0.9}}
	tw := []float64{w[0], w[1], termsW, w[2+terms], w[3+terms], w[4+terms]}
	h1 := 0.0
	for i, b := range top {
		if i == 2 {
			continue
		}
		if bh := p.height(b, tw[i]); bh > h1 {
			h1 = bh
		}
	}
	half := lineH(p.size) + 2*pad
	if h1 < 2*half {
		h1 = 2 * half
	}
	cx := x
	for i, b := range top {
		b.align, b.border, b.fill = "C", true, grey
		if i == 2 {
			p.draw(b, cx, y, tw[i], h1-half)
			for t := range terms {
				p.draw(box{text: strconv.Itoa(t + 1), bold: true, align: "C", border: true, fill: grey}, cx+sumTo(w[2:], t), y+h1-half, w[2+t], half)
			}
		} else {
			p.draw(b, cx, y, tw[i], h1)
		}
		cx += tw[i]
	}
	return h1
}

func sumTo(ws []float64, n int) float64 {
	s := 0.0
	for i := 0; i < n; i++ {
		s += ws[i]
	}
	return s
}

// areaBlock draws an area with its competencies; the area name, the area's annual cell and the final conclusion
// span all its rows
func areaBlock(p *pen, x, y float64, a port.CardAreaDoc, terms int) float64 {
	w := gridWidths(terms)
	heights := make([]float64, len(a.Competencies))
	total := 0.0
	for i, c := range a.Competencies {
		h := p.height(box{text: c.Name, scale: 0.92}, w[1])
		heights[i] = h
		total += h
	}
	// The spanning cells may need more room than the rows
	if need := p.height(box{text: a.Name, bold: true}, w[0]); need > total {
		heights[len(heights)-1] += need - total
		total = need
	}
	if need := p.height(box{text: a.FinalConclusion, scale: 0.85}, w[4+terms]); need > total {
		heights[len(heights)-1] += need - total
		total = need
	}
	p.draw(box{text: a.Name, bold: true, align: "C", border: true}, x, y, w[0], total)
	cy := y
	for i, c := range a.Competencies {
		cx := x + w[0]
		p.draw(box{text: c.Name, scale: 0.92, border: true}, cx, cy, w[1], heights[i])
		cx += w[1]
		for t := range terms {
			level := ""
			if t < len(c.Levels) {
				level = c.Levels[t]
			}
			p.draw(box{text: level, bold: true, align: "C", border: true, nowrap: true}, cx, cy, w[2+t], heights[i])
			cx += w[2+t]
		}
		p.draw(box{text: c.Annual, bold: true, align: "C", border: true, nowrap: true}, cx, cy, w[2+terms], heights[i])
		cy += heights[i]
	}
	ax := x + faceW - w[4+terms] - w[3+terms]
	var fill *port.Colour
	if a.Transversal {
		fill = shaded
	}
	p.draw(box{border: true, fill: fill}, ax, y, w[3+terms], total)
	p.draw(box{text: a.FinalConclusion, scale: 0.85, border: true, top: true}, ax+w[3+terms], y, w[4+terms], total)
	return total
}

// cardHeader draws the title, the national coat of arms, the data grid and the institution's logo
func cardHeader(p *pen, c port.ReportCardDoc, logo string, x, y float64) float64 {
	h := p.text(x, y, faceW, "INFORME DE PROGRESO DEL ESTUDIANTE – "+c.Year, true, 1.6, "C")
	if c.Provisional {
		h += p.text(x, y+h, faceW, "Documento provisional: el periodo de evaluación aún está en curso.", false, 0.9, "C")
	}
	h += 1.5
	imgW := 20.0
	gx, gw := x+imgW+3, faceW-2*imgW-6
	w := cols(gw, 24, 26, 24, 26)
	pair := func(l1, v1, l2, v2 string) []box {
		return []box{{text: l1, scale: 0.92, border: true}, {text: v1, bold: true, border: true}, {text: l2, scale: 0.92, border: true}, {text: v2, bold: true, border: true}}
	}
	wide := func(l, v string) ([]float64, []box) {
		return []float64{w[0], w[1] + w[2] + w[3]}, []box{{text: l, scale: 0.92, border: true}, {text: v, bold: true, border: true}}
	}
	gy := y + h
	start := gy
	gy += p.row(gx, gy, w, pair("DRE / GRE:", c.Dre, "UGEL:", c.Ugel), 0)
	gy += p.row(gx, gy, w, pair("Nivel:", strings.ToUpper(c.Level), "Código modular:", c.ModularCode), 0)
	ww, bb := wide("Institución educativa:", c.Institution)
	gy += p.row(gx, gy, ww, bb, 0)
	gy += p.row(gx, gy, w, pair("Grado:", c.Grade, "Sección:", c.Section), 0)
	ww, bb = wide("Apellidos y nombres del estudiante:", c.Student)
	gy += p.row(gx, gy, ww, bb, 0)
	gy += p.row(gx, gy, w, pair("Código del estudiante:", c.Code, "DNI:", c.Document), 0)
	ww, bb = wide("Apellidos y nombres del docente o tutor:", c.Tutor)
	gy += p.row(gx, gy, ww, bb, 0)
	gridH := gy - start
	if !p.dry {
		fit(p.pdf, "escudo", x, start, imgW, gridH)
		fit(p.pdf, logo, x+faceW-imgW, start, imgW, gridH)
	}
	return h + gridH + 2
}

// cardBack draws the conclusions, the tutor's conclusion per term, the attendance with conduct, the CNEB scale and
// the signatures; returns the height used
func cardBack(p *pen, c port.ReportCardDoc, x, y float64) float64 {
	start := y
	// Conclusions of the selected term
	w := cols(faceW, 12, 34, 54)
	y += p.row(x, y, w, []box{{text: "Periodo", bold: true, align: "C", border: true, fill: grey}, {text: "Competencia", bold: true, align: "C", border: true, fill: grey}, {text: "Conclusión descriptiva", bold: true, align: "C", border: true, fill: grey}}, 0)
	if len(c.Conclusions) == 0 {
		y += p.row(x, y, w, []box{{border: true}, {border: true}, {border: true}}, lineH(p.size)+2*pad)
	} else {
		first := y
		for _, k := range c.Conclusions {
			y += p.row(x+w[0], y, w[1:], []box{{text: k.Competency, scale: 0.9, border: true}, {text: k.Text, scale: 0.9, border: true}}, 0)
		}
		p.draw(box{text: strconv.Itoa(c.TermNumber), bold: true, align: "C", border: true}, x, first, w[0], y-first)
	}
	y += 3
	// The tutor's conclusion of each term
	w = cols(faceW, 12, 88)
	y += p.row(x, y, w, []box{{text: "Periodo", bold: true, align: "C", border: true, fill: grey}, {text: "Conclusión descriptiva por periodo", bold: true, align: "C", border: true, fill: grey}}, 0)
	for i, t := range c.Terms {
		y += p.row(x, y, w, []box{{text: strconv.Itoa(i + 1), bold: true, align: "C", border: true}, {text: t.Comment, scale: 0.9, border: true}}, 1.8*lineH(p.size))
	}
	y += 3
	// Attendance (left) and the CNEB scale (right)
	leftW := faceW * 0.5
	rightX, rightW := x+leftW+4, faceW-leftW-4
	ly := y + p.text(x, y, leftW, "Resumen de asistencia del estudiante", true, 1, "C") + 1
	conduct := false
	for _, t := range c.Terms {
		conduct = conduct || t.Conduct != nil
	}
	rel := []float64{16, 17, 17, 17, 17}
	if conduct {
		rel = append(rel, 16)
	}
	aw := cols(leftW, rel...)
	head := lineH(p.size) + 2*pad
	p.draw(box{text: "Periodo", bold: true, align: "C", border: true, fill: grey, nowrap: true}, x, ly, aw[0], 2*head)
	p.draw(box{text: "Inasistencias", bold: true, align: "C", border: true, fill: grey}, x+aw[0], ly, aw[1]+aw[2], head)
	p.draw(box{text: "Tardanzas", bold: true, align: "C", border: true, fill: grey}, x+aw[0]+aw[1]+aw[2], ly, aw[3]+aw[4], head)
	if conduct {
		p.draw(box{text: "Conducta", bold: true, align: "C", border: true, fill: grey, nowrap: true}, x+sumTo(aw, 5), ly, aw[5], 2*head)
	}
	cx := x + aw[0]
	for i, l := range []string{"Justificadas", "Injustificadas", "Justificadas", "Injustificadas"} {
		p.draw(box{text: l, bold: true, scale: 0.8, align: "C", border: true, fill: grey, nowrap: true}, cx, ly+head, aw[1+i], head)
		cx += aw[1+i]
	}
	ly += 2 * head
	for i, t := range c.Terms {
		cells := []box{{text: strconv.Itoa(i + 1), bold: true, align: "C", border: true}}
		vals := []string{"", "", "", ""}
		if !t.Empty {
			// Approved justifications turn a late arrival into a justified absence, so no late stays justified
			vals = []string{strconv.Itoa(t.Justified), strconv.Itoa(t.Absent), "0", strconv.Itoa(t.Late)}
		}
		for _, v := range vals {
			cells = append(cells, box{text: v, align: "C", border: true})
		}
		if conduct {
			v := ""
			if t.Conduct != nil {
				v = strconv.Itoa(*t.Conduct)
			}
			cells = append(cells, box{text: v, bold: true, align: "C", border: true})
		}
		ly += p.row(x, ly, aw, cells, 0)
	}
	ry := y + p.text(rightX, y, rightW, "ESCALA DE CALIFICACIONES DEL CNEB", true, 1, "C") + 1
	sw := cols(rightW, 10, 90)
	for _, s := range scaleRows {
		ry += p.row(rightX, ry, sw, []box{{text: s[0], bold: true, scale: 1.1, align: "C", border: true, nowrap: true}, {text: s[1] + "\n" + s[2], scale: 0.78, border: true}}, 0)
	}
	y = max(ly, ry) + 9
	// Signatures and issue date
	sigW := (faceW - 12) / 2
	if !p.dry {
		p.pdf.Line(x, y, x+sigW, y)
		p.pdf.Line(x+sigW+12, y, x+faceW, y)
	}
	p.text(x, y+0.5, sigW, "Firma y sello del docente o tutor(a)", false, 1.1, "C")
	y += 0.5 + p.text(x+sigW+12, y+0.5, sigW, "Firma y sello del director(a)", false, 1.1, "C") + 1.5
	y += p.text(x, y, faceW, "Emitido: "+longDate(c.IssuedOn)+"\nGenerado con ListerCore Edu", false, 0.85, "R")
	return y - start
}

// literalCard lays the card out at the largest size (8 pt down to 6 pt) where both faces fit on the sheet: areas
// fill the front in order and the rest, with the transversal competencies, go to the back
func literalCard(pdf *fpdf.Fpdf, c port.ReportCardDoc) {
	logo := image(pdf, logoName(c.Logo), c.Logo)
	terms := len(c.Terms)
	var front, back []port.CardAreaDoc
	size := 8.0
	for ; size >= 6; size -= 0.25 {
		p := &pen{pdf: pdf, size: size, dry: true}
		y := margin + cardHeader(p, c, "", margin, margin)
		y += gridHeader(p, margin, y, terms)
		front, back = nil, nil
		for _, a := range c.Areas {
			h := areaBlock(p, margin, y, a, terms)
			if len(back) == 0 && (y+h <= margin+faceH || len(front) == 0) {
				front = append(front, a)
				y += h
			} else {
				back = append(back, a)
			}
		}
		if y > margin+faceH {
			continue
		}
		by := margin
		if len(back) > 0 {
			by += gridHeader(p, margin, by, terms)
			for _, a := range back {
				by += areaBlock(p, margin, by, a, terms)
			}
			by += 3
		}
		if by+cardBack(p, c, margin, by) <= margin+faceH {
			break
		}
	}
	p := &pen{pdf: pdf, size: max(size, 6)}
	image(pdf, "escudo", port.Image{Data: escudo, Type: "png"})
	y := margin + cardHeader(p, c, logo, margin, margin)
	y += gridHeader(p, margin, y, terms)
	for _, a := range front {
		y += areaBlock(p, margin, y, a, terms)
	}
	bx := margin + faceW + gap
	by := margin
	if len(back) > 0 {
		by += gridHeader(p, bx, by, terms)
		for _, a := range back {
			by += areaBlock(p, bx, by, a, terms)
		}
		by += 3
	}
	cardBack(p, c, bx, by)
}

// numericCard is the grades list of institutes and academies (A4 portrait)
func numericCard(pdf *fpdf.Fpdf, c port.ReportCardDoc) {
	logo := image(pdf, logoName(c.Logo), c.Logo)
	image(pdf, "escudo", port.Image{Data: escudo, Type: "png"})
	p := &pen{pdf: pdf, size: 11}
	x, w := margin, 210-2*margin
	fit(pdf, "escudo", x, margin, 16, 18)
	fit(pdf, logo, x+w-16, margin, 16, 18)
	y := margin + p.text(x+20, margin, w-40, c.Institution, true, 1, "C")
	y += p.text(x+20, y, w-40, "BOLETA DE NOTAS", true, 1.6, "C")
	y += p.text(x+20, y, w-40, strings.TrimSpace(c.Period+" "+c.TermName), false, 1, "C")
	official := strings.Join(nonEmpty(c.Dre, c.Ugel, prefixed("Código modular ", c.ModularCode)), " · ")
	if official != "" {
		y += p.text(x+20, y, w-40, official, false, 0.85, "C")
	}
	y = max(y, margin+19) + 3
	if c.Provisional {
		y += p.text(x, y, w, "Notas en curso: no es definitivo.", false, 0.9, "L")
	}
	iw := cols(w, 22, 78)
	for _, kv := range [][2]string{{"Estudiante", c.Student}, {"Código / DNI", strings.Join(nonEmpty(c.Code, c.Document), " · ")}, {"Sección", strings.Join(nonEmpty(c.Grade+" "+c.Section, c.Level), " · ")}, {"Tutor(a)", c.Tutor}} {
		y += p.row(x, y, iw, []box{{text: kv[0], border: true, fill: grey}, {text: kv[1], bold: true, border: true}}, 0)
	}
	y += 4
	rel := []float64{30, 42, 14}
	if c.Passing {
		rel = append(rel, 14)
	}
	sw := cols(w, rel...)
	head := []box{{text: c.SubjectHead, bold: true, border: true, fill: grey}, {text: "Evaluaciones", bold: true, border: true, fill: grey}, {text: "Promedio", bold: true, align: "C", border: true, fill: grey}}
	if c.Passing {
		head = append(head, box{text: "Situación", bold: true, align: "C", border: true, fill: grey})
	}
	y += p.row(x, y, sw, head, 0)
	for _, s := range c.Subjects {
		name := s.Name
		if s.Teacher != "" {
			name += "\n" + s.Teacher
		}
		cells := []box{{text: name, border: true}, {text: s.Evaluations, scale: 0.85, border: true}, {text: s.Grade, bold: true, scale: 1.2, align: "C", border: true}}
		if c.Passing {
			cells = append(cells, box{text: s.Situation, align: "C", border: true})
		}
		if y+p.height(cells[0], sw[0])+p.height(cells[1], sw[1]) > 297-margin-30 {
			pdf.AddPageFormat("P", fpdf.SizeType{Wd: 210, Ht: 297})
			y = margin
		}
		y += p.row(x, y, sw, cells, 0)
	}
	y += 4
	aw := cols(w, 34, 22, 22, 22)
	y += p.row(x, y, aw, []box{{text: "Asistencia", bold: true, border: true, fill: grey}, {text: "Tardanzas", bold: true, align: "C", border: true, fill: grey}, {text: "Faltas", bold: true, align: "C", border: true, fill: grey}, {text: "Justificadas", bold: true, align: "C", border: true, fill: grey}}, 0)
	for _, t := range c.Terms {
		y += p.row(x, y, aw, []box{{text: t.Name, border: true}, {text: strconv.Itoa(t.Late), align: "C", border: true}, {text: strconv.Itoa(t.Absent), align: "C", border: true}, {text: strconv.Itoa(t.Justified), align: "C", border: true}}, 0)
	}
	y = max(y+22, 297-margin-22)
	sigW := (w - 20) / 2
	pdf.Line(x, y, x+sigW, y)
	pdf.Line(x+sigW+20, y, x+w, y)
	p.text(x, y+1, sigW, "Coordinación académica", false, 1, "C")
	p.text(x+sigW+20, y+1, sigW, "Dirección", false, 1, "C")
	p.text(x, 297-margin-5, w, "Emitido: "+longDate(c.IssuedOn)+" · Generado con ListerCore Edu", false, 0.8, "R")
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

func prefixed(prefix, v string) string {
	if v == "" {
		return ""
	}
	return prefix + v
}
