package pdf

import (
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"

	"api/internal/core/port"
)

const fullW = pageW - 2*margin

// Schedules draws one A4 landscape sheet per page of the document
func (r *Renderer) Schedules(doc port.ScheduleDoc) ([]byte, error) {
	pdf := newDoc(true, "Horarios")
	logo := image(pdf, logoName(doc.Logo), doc.Logo)
	for i, pg := range doc.Pages {
		pdf.AddPage()
		at := sheetAt{logo: logo, number: i + 1, total: len(doc.Pages)}
		// The largest size where the page fits: grids, legends and the seal shrink together
		size := 10.0
		for ; size > 5.5; size -= 0.25 {
			if schedulePage(&pen{pdf: pdf, size: size, dry: true}, doc, pg, at) <= pageH-margin {
				break
			}
		}
		schedulePage(&pen{pdf: pdf, size: size}, doc, pg, at)
		if doc.Seal != nil {
			watermark(pdf, doc.Seal.Mark, pageW, pageH)
		}
	}
	if len(doc.Pages) == 0 {
		pdf.AddPage()
	}
	return output(pdf)
}

// sheetAt is where a sheet sits in the document: the institution's logo (registered name, "" for none) and its
// page number out of the total
type sheetAt struct {
	logo          string
	number, total int
}

// Size of the logo box in the header, in millimetres
const headerLogo = 14.0

// schedulePage draws (or measures) a sheet and returns where it ends: header (logo, institution, title), the grids
// side by side (as narrow as their columns allow, centred), the legends below them, the verification block and the
// page number at the foot
func schedulePage(p *pen, doc port.ScheduleDoc, pg port.SchedulePage, at sheetAt) float64 {
	x, y := margin, margin
	tx := x
	if at.logo != "" {
		if !p.dry {
			fit(p.pdf, at.logo, x, y, headerLogo, headerLogo)
		}
		tx = x + headerLogo + 3
	}
	tw := fullW - (tx - x)
	ih := p.text(tx, y, tw, strings.ToUpper(doc.Institution), false, 0.9, "L")
	th := p.text(tx, y+ih, tw*0.65, pg.Title, true, 2, "L")
	p.text(x+fullW*0.4, y+ih+th-lineH(p.size*1.15), fullW*0.6, pg.Subtitle, true, 1.15, "R")
	y += ih + th
	if at.logo != "" {
		y = max(y, margin+headerLogo)
	}
	y += 0.5
	if !p.dry {
		p.pdf.SetLineWidth(0.6)
		p.pdf.Line(x, y, x+fullW, y)
		p.pdf.SetLineWidth(0.2)
	}
	y += 2
	if pg.Info != "" {
		y += p.text(x, y, fullW, pg.Info, false, 0.95, "L") + 1.5
	}
	footer := lineH(p.size*0.8) + 1.5
	bottom := pageH - margin - footer
	sealH := 0.0
	if doc.Seal != nil {
		sealH = seal(&pen{pdf: p.pdf, size: p.size, dry: true}, x, 0, fullW, *doc.Seal) + 2.5
	}
	n := max(len(pg.Grids), 1)
	gapX := 5.0
	slot := (fullW - gapX*float64(n-1)) / float64(n)
	total := 0.0
	widths := make([]float64, len(pg.Grids))
	for i, g := range pg.Grids {
		widths[i] = gridWidth(g, slot)
		total += widths[i]
	}
	total += gapX * float64(len(pg.Grids)-1)
	// Legends span the grids, never narrower than a readable table
	lw := min(fullW, max(total, 170))
	lx := x + (fullW-lw)/2
	legendsH := 0.0
	for _, l := range pg.Legends {
		legendsH += legend(&pen{pdf: p.pdf, size: p.size, dry: true}, lx, y, lw, l) + 2.5
	}
	gh := 0.0
	gx := x + (fullW-total)/2
	for i, g := range pg.Grids {
		gh = max(gh, grid(p, gx, y, widths[i], g, bottom-sealH-y-legendsH-3))
		gx += widths[i] + gapX
	}
	if len(pg.Grids) > 0 {
		y += gh + 3
	}
	for _, l := range pg.Legends {
		y += legend(p, lx, y, lw, l) + 2.5
	}
	if doc.Seal != nil && !p.dry {
		seal(p, x, bottom-sealH+2, fullW, *doc.Seal)
	}
	issued := "Generado el " + longDate(doc.IssuedOn)
	if doc.Seal != nil && doc.Seal.Version > 0 {
		issued = "Versión " + strconv.Itoa(doc.Seal.Version) + " · " + issued
	}
	p.text(x, pageH-margin-footer+1.5, fullW, issued+" · joseph", false, 0.8, "R")
	if at.total > 0 {
		p.text(x, pageH-margin-footer+1.5, fullW, "Página "+strconv.Itoa(at.number)+" de "+strconv.Itoa(at.total), true, 0.8, "L")
	}
	return y + sealH + footer
}

// gridWidth is how wide a grid prints inside a slot: the hour column plus narrow columns, capped so a single
// week does not stretch across the sheet
func gridWidth(g port.ScheduleGrid, slot float64) float64 {
	head := min(15, slot*0.14)
	sub := max(len(g.SubColumns), 1)
	leaves := float64(max(len(g.Columns)*sub, 1))
	limit := 34.0
	if len(g.SubColumns) > 0 {
		limit = max(13, 36/float64(sub))
	}
	return head + leaves*min((slot-head)/leaves, limit)
}

// grid draws a timetable: the day headers (and their narrow columns), then class rows and breaks. Class rows grow
// to fill maxH, up to a bit more than twice their natural height
func grid(p *pen, x, y, w float64, g port.ScheduleGrid, maxH float64) float64 {
	start := y
	if g.Caption != "" {
		y += p.text(x, y, w, g.Caption, true, 0.95, "L") + 0.8
	}
	headW := min(15, w*0.14)
	sub := max(len(g.SubColumns), 1)
	leaves := max(len(g.Columns)*sub, 1)
	leafW := (w - headW) / float64(leaves)
	dayW := leafW * float64(sub)
	h1 := lineH(p.size*0.9) + 2*pad
	h2 := 0.0
	if len(g.SubColumns) > 0 {
		h2 = lineH(p.size*0.75) + 2*pad
	}
	p.draw(box{text: g.Corner, bold: true, scale: 0.9, align: "C", border: true, fill: grey}, x, y, headW, h1+h2)
	for i, c := range g.Columns {
		cx := x + headW + float64(i)*dayW
		p.draw(box{text: c, bold: true, scale: 0.9, align: "C", border: true, fill: grey, nowrap: true}, cx, y, dayW, h1)
		for j, s := range g.SubColumns {
			p.draw(box{text: s, bold: true, scale: 0.75, align: "C", border: true, fill: grey, nowrap: true}, cx+float64(j)*leafW, y+h1, leafW, h2)
		}
	}
	y += h1 + h2
	codeH, detailH := lineH(p.size*1.15), lineH(p.size*0.72)
	rowH := codeH + detailH + 2*pad
	breakH := lineH(p.size*0.8) + 2*pad
	classes, breaks := 0, 0
	for _, r := range g.Rows {
		if r.Break {
			breaks++
		} else {
			classes++
		}
	}
	if classes > 0 {
		if fill := (maxH - (y - start) - float64(breaks)*breakH) / float64(classes); fill > rowH {
			rowH = min(fill, rowH*2.4)
		}
	}
	for _, r := range g.Rows {
		if r.Break {
			p.draw(box{text: r.Head, scale: 0.8, align: "C", border: true}, x, y, headW, breakH)
			p.draw(box{text: "R E C R E O", scale: 0.8, align: "C", border: true, fill: &port.Colour{R: 243, G: 243, B: 243}}, x+headW, y, w-headW, breakH)
			y += breakH
			continue
		}
		h := max(rowH, p.height(box{text: r.Head, scale: 0.8}, headW))
		p.draw(box{text: r.Head, scale: 0.8, align: "C", border: true}, x, y, headW, h)
		for i, c := range r.Cells {
			cx := x + headW + float64(i)*leafW
			p.draw(box{fill: c.Fill, border: true}, cx, y, leafW, h)
			if c.Code == "" || p.dry {
				continue
			}
			lines := codeH
			if c.Detail != "" {
				lines += detailH
			}
			mid := y + (h-lines)/2
			fitLine(p, c.Code, true, 1.15, cx, mid, leafW, codeH)
			if c.Detail != "" {
				fitLine(p, c.Detail, false, 0.72, cx, mid+codeH, leafW, detailH)
			}
		}
		y += h
	}
	return y - start
}

// fitLine writes one centred line in a cell, shrinking the font when the cell is narrow; details that still do
// not fit at the smallest size are shortened with an ellipsis
func fitLine(p *pen, s string, bold bool, scale, x, y, w, h float64) {
	style := ""
	if bold {
		style = "B"
	}
	inner := w - 2*pad*0.6
	size := p.size * scale
	for size > p.size*scale*0.55 {
		p.pdf.SetFont(family, style, size)
		if p.pdf.GetStringWidth(s) <= inner {
			break
		}
		size -= 0.25
	}
	p.pdf.SetFont(family, style, size)
	p.pdf.SetXY(x+pad*0.6, y)
	p.pdf.CellFormat(inner, h, clip(p.pdf, s, inner), "", 0, "CM", false, 0, "")
}

// clip shortens a text with an ellipsis until it fits a width
func clip(pdf *fpdf.Fpdf, s string, w float64) string {
	if pdf.GetStringWidth(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 1 && pdf.GetStringWidth(string(r)+"…") > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// legend draws a legend table; cells with a fill carry the course's colour chip
func legend(p *pen, x, y, w float64, l port.LegendTable) float64 {
	start := y
	if l.Title != "" {
		y += p.text(x, y, w, l.Title, true, 1, "L") + 0.8
	}
	rel := l.Widths
	if len(rel) != len(l.Columns) {
		rel = make([]float64, len(l.Columns))
		for i := range rel {
			rel[i] = 1
		}
	}
	widths := cols(w, rel...)
	if len(l.Columns) > 0 {
		head := make([]box, len(l.Columns))
		for i, c := range l.Columns {
			head[i] = box{text: c, bold: true, scale: 0.9, align: "C", border: true, fill: grey, nowrap: true}
		}
		y += p.row(x, y, widths, head, 0)
	}
	for _, r := range l.Rows {
		cells := make([]box, len(r.Cells))
		chipped := func(i int) bool { return i < len(r.Fills) && r.Fills[i] != nil }
		for i, c := range r.Cells {
			cells[i] = box{text: c, scale: 0.85, border: true}
			if chipped(i) {
				// Room for the chip before a code
				cells[i].bold = true
				cells[i].text = "     " + c
				cells[i].nowrap = true
			}
		}
		h := p.row(x, y, widths, cells, 0)
		if !p.dry {
			chip := lineH(p.size * 0.65)
			cx := x
			for i := range r.Cells {
				if chipped(i) {
					f := r.Fills[i]
					p.pdf.SetFillColor(f.R, f.G, f.B)
					p.pdf.Rect(cx+1.2, y+(h-chip)/2, chip, chip, "FD")
				}
				cx += widths[i]
			}
		}
		y += h
	}
	return y - start
}
