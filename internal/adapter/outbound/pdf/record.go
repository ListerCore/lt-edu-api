package pdf

import (
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"

	"api/internal/core/port"
)

// Record draws an issued acta on A4 landscape: header, the register (one column per competency or evaluation,
// header repeated on every page), what each column means, the legend, the summary and the signatures
func (r *Renderer) Record(doc port.RecordDoc) ([]byte, error) {
	pdf := newDoc(true, doc.Title)
	pdf.AddPage()
	image(pdf, "escudo", port.Image{Data: escudo, Type: "png"})
	columns := 0
	for _, g := range doc.Groups {
		columns += len(g.Codes)
	}
	// Smaller type when the register is wide
	size := 9.0
	if columns > 30 {
		size = 7
	} else if columns > 18 {
		size = 8
	}
	p := &pen{pdf: pdf, size: size}
	x := margin
	fit(pdf, "escudo", x, margin, 14, 16)
	y := margin + p.text(x+17, margin, fullW-80, doc.Institution, true, 1, "L")
	y += p.text(x+17, y, fullW-80, doc.Title, true, 1.5, "L")
	y += p.text(x+17, y, fullW-80, doc.Subtitle, false, 1, "L")
	ry := margin + p.text(x+fullW-80, margin, 80, doc.Code, true, 1.2, "R")
	ry += p.text(x+fullW-80, ry, 80, "Emitida el "+doc.Issued, false, 0.95, "R")
	if doc.Replaced {
		p.text(x+fullW-80, ry, 80, "Reemplazada por una emisión posterior", true, 0.95, "R")
	}
	y = max(y, margin+17) + 1
	if len(doc.Info) > 0 {
		y += p.text(x, y, fullW, strings.Join(doc.Info, " · "), false, 0.95, "L") + 1.5
	}
	// Columns: N.°, code, name, the groups' columns, situation
	nameW := 52.0
	fixed := 8 + 18 + nameW + 26
	cw := (fullW - fixed) / float64(max(columns, 1))
	widths := []float64{8, 18, nameW}
	for range columns {
		widths = append(widths, cw)
	}
	widths = append(widths, 26)
	header := func(y float64) float64 {
		h := lineH(size) + 2*pad
		gh := h
		for _, g := range doc.Groups {
			w := cw * float64(len(g.Codes))
			gh = max(gh, p.height(box{text: g.Name, bold: true, scale: 0.85}, w))
		}
		total := gh + h
		p.draw(box{text: "N.°", bold: true, align: "C", border: true, fill: grey}, x, y, 8, total)
		p.draw(box{text: "Código", bold: true, align: "C", border: true, fill: grey}, x+8, y, 18, total)
		p.draw(box{text: "Apellidos y nombres", bold: true, align: "C", border: true, fill: grey}, x+26, y, nameW, total)
		cx := x + 26 + nameW
		for _, g := range doc.Groups {
			w := cw * float64(len(g.Codes))
			p.draw(box{text: g.Name, bold: true, scale: 0.85, align: "C", border: true, fill: grey}, cx, y, w, gh)
			for i, c := range g.Codes {
				p.draw(box{text: c, scale: 0.85, align: "C", border: true, fill: grey}, cx+float64(i)*cw, y+gh, cw, h)
			}
			cx += w
		}
		p.draw(box{text: "Situación", bold: true, align: "C", border: true, fill: grey}, cx, y, 26, total)
		return total
	}
	y += header(y)
	bottom := pageH - margin - 6
	for _, r := range doc.Rows {
		name := r.Name
		if r.Note != "" {
			name += "\n" + r.Note
		}
		cells := []box{{text: strconv.Itoa(r.N), align: "C", border: true}, {text: r.Code, scale: 0.9, border: true}, {text: name, border: true}}
		for _, c := range r.Cells {
			cells = append(cells, box{text: c, bold: c == "C" || c == "AD", align: "C", border: true})
		}
		cells = append(cells, box{text: r.Situation, bold: true, scale: 0.9, align: "C", border: true})
		h := p.height(cells[2], nameW)
		if y+h > bottom {
			pdf.AddPage()
			y = margin + header(margin)
		}
		y += p.row(x, y, widths, cells, 0)
	}
	y += 3
	tail := func(need float64) {
		if y+need > bottom {
			pdf.AddPage()
			y = margin
		}
	}
	if len(doc.Columns) > 0 {
		tail(20)
		y += p.text(x, y, fullW, "Columnas: "+strings.Join(doc.Columns, " · "), false, 0.85, "L") + 1.5
	}
	if doc.Legend != "" {
		y += p.text(x, y, fullW, doc.Legend, false, 0.85, "L") + 1.5
	}
	if len(doc.Summary) > 0 {
		y += p.text(x, y, fullW, "Resumen: "+strings.Join(doc.Summary, " · "), true, 0.95, "L")
	}
	signatures(pdf, p, doc.Signatures, &y)
	return output(pdf)
}

// signatures draws the signature lines spread across the page, on a new page when they do not fit
func signatures(pdf *fpdf.Fpdf, p *pen, names []string, y *float64) {
	if len(names) == 0 {
		return
	}
	if *y+22 > pageH-margin {
		pdf.AddPage()
		*y = margin
	}
	line := *y + 16
	gapX := 20.0
	w := (fullW - gapX*float64(len(names)-1)) / float64(len(names))
	for i, n := range names {
		lx := margin + float64(i)*(w+gapX)
		pdf.Line(lx, line, lx+w, line)
		p.text(lx, line+1, w, n, false, 1, "C")
	}
	*y = line + 6
}
