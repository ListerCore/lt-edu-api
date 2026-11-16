package pdf

import (
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"

	"api/internal/core/port"
)

// Forms and lists of the secretariat: the ficha única and the acta de ubicación (portrait forms) and the nómina
// (a landscape list)

const (
	portraitW = 210.0
	portraitH = 297.0
)

// docHeader draws the official heading (escudo, DRE and UGEL, institution and modular code, the logo on the right,
// the title and the code) and returns where the body starts
func docHeader(pdf *fpdf.Fpdf, p *pen, h port.DocHeader, w float64) float64 {
	x := margin
	image(pdf, "escudo", port.Image{Data: escudo, Type: "png"})
	fit(pdf, "escudo", x, margin, 13, 15)
	logo := image(pdf, logoName(h.Logo), h.Logo)
	right := 0.0
	if logo != "" {
		fit(pdf, logo, x+w-15, margin, 15, 15)
		right = 17
	}
	tx, tw := x+16, w-16-right
	y := margin
	if line := strings.Join(nonBlank(h.Dre, h.Ugel), " · "); line != "" {
		y += p.text(tx, y, tw, line, false, 0.85, "L")
	}
	y += p.text(tx, y, tw, h.Institution, true, 1.05, "L")
	if h.ModularCode != "" {
		y += p.text(tx, y, tw, "Código modular "+h.ModularCode, false, 0.85, "L")
	}
	y = max(y, margin+15) + 3
	y += p.text(x, y, w, h.Title, true, 1.45, "C")
	if h.Subtitle != "" {
		y += p.text(x, y, w, h.Subtitle, false, 1, "C")
	}
	if h.Code != "" {
		y += p.text(x, y, w, h.Code, true, 1, "C")
	}
	return y + 3
}

func nonBlank(values ...string) []string {
	out := []string{}
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// Form draws a portrait form, breaking pages between fields when needed
func (r *Renderer) Form(doc port.FormDoc) ([]byte, error) {
	pdf := newDoc(false, doc.Header.Title)
	const w = portraitW - 2*margin
	p := &pen{pdf: pdf, size: 10}
	issued := "Generado el " + longDate(doc.IssuedOn)
	if doc.Seal != nil && doc.Seal.Version > 0 {
		issued = "Versión " + strconv.Itoa(doc.Seal.Version) + " · " + issued
	}
	footer(pdf, p, w, portraitH, issued)
	pdf.AddPage()
	bottom := portraitH - margin - 6
	y := docHeader(pdf, p, doc.Header, w)
	need := func(h float64) {
		if y+h > bottom {
			pdf.AddPage()
			y = margin
		}
	}
	for _, b := range doc.Blocks {
		need(14)
		y += p.row(margin, y, []float64{w}, []box{{text: b.Title, bold: true, fill: grey, border: true}}, 0)
		cols := max(1, min(b.Columns, 2))
		cw := w / float64(cols)
		widths := cols2(cw)
		for i := 0; i < len(b.Fields); i += cols {
			rowH := 0.0
			for c := 0; c < cols && i+c < len(b.Fields); c++ {
				f := b.Fields[i+c]
				rowH = max(rowH, (&pen{pdf: pdf, size: p.size, dry: true}).row(0, 0, widths, fieldBoxes(f), 0))
			}
			need(rowH)
			for c := 0; c < cols && i+c < len(b.Fields); c++ {
				p.row(margin+float64(c)*cw, y, widths, fieldBoxes(b.Fields[i+c]), rowH)
			}
			y += rowH
		}
		y += 3
	}
	for _, para := range doc.Paragraphs {
		h := (&pen{pdf: pdf, size: p.size, dry: true}).text(margin, 0, w, para, false, 1, "L")
		need(h)
		y += p.text(margin, y, w, para, false, 1, "L") + 2
	}
	if doc.Seal != nil {
		sealH := seal(&pen{pdf: pdf, size: p.size, dry: true}, margin, 0, w, *doc.Seal)
		need(sealH + 4)
		seal(p, margin, max(y+4, bottom-sealH), w, *doc.Seal)
		if doc.Seal.Mark != "" {
			watermark(pdf, doc.Seal.Mark, portraitW, portraitH)
		}
	} else if len(doc.Signatures) > 0 {
		need(26)
		line := max(y+20, bottom-8)
		gap := 16.0
		sw := (w - gap*float64(len(doc.Signatures)-1)) / float64(len(doc.Signatures))
		for i, n := range doc.Signatures {
			lx := margin + float64(i)*(sw+gap)
			pdf.Line(lx, line, lx+sw, line)
			p.text(lx, line+1, sw, n, false, 0.9, "C")
		}
	}
	return output(pdf)
}

// cols2 splits a field column into its label and value
func cols2(w float64) []float64 { return cols(w, 36, 64) }

func fieldBoxes(f port.FormField) []box {
	value := f.Value
	if value == "" {
		value = "—"
	}
	return []box{{text: f.Label, scale: 0.85, border: true}, {text: value, bold: true, border: true}}
}

// Table draws a landscape list: each group starts a page with the heading, its info and the column header, which
// repeats when the group runs over
func (r *Renderer) Table(doc port.TableDoc) ([]byte, error) {
	pdf := newDoc(true, doc.Header.Title)
	p := &pen{pdf: pdf, size: 8.5}
	footer(pdf, p, fullW, pageH, "Generado el "+longDate(doc.IssuedOn))
	widths := cols(fullW, doc.Widths...)
	header := make([]box, len(doc.Columns))
	for i, c := range doc.Columns {
		header[i] = box{text: c, bold: true, fill: grey, border: true, align: "C", scale: 0.95}
	}
	bottom := pageH - margin - 8
	groups := doc.Groups
	if len(groups) == 0 {
		groups = []port.TableGroup{{Title: "Sin registros"}}
	}
	for _, g := range groups {
		pdf.AddPage()
		h := doc.Header
		h.Subtitle = strings.Join(nonBlank(h.Subtitle, g.Title), " · ")
		y := docHeader(pdf, p, h, fullW)
		if len(g.Info) > 0 {
			y += p.text(margin, y, fullW, strings.Join(g.Info, "   ·   "), false, 0.95, "L") + 1.5
		}
		y += p.row(margin, y, widths, header, 0)
		for _, row := range g.Rows {
			boxes := make([]box, len(row))
			for i, c := range row {
				align := "L"
				if i == 0 {
					align = "C"
				}
				boxes[i] = box{text: c, border: true, align: align}
			}
			rh := (&pen{pdf: pdf, size: p.size, dry: true}).row(0, 0, widths, boxes, 0)
			if y+rh > bottom {
				pdf.AddPage()
				y = margin
				y += p.row(margin, y, widths, header, 0)
			}
			y += p.row(margin, y, widths, boxes, rh)
		}
		if g.Total != "" {
			y += 1.5
			y += p.text(margin, y, fullW, g.Total, true, 1, "L")
		}
		if doc.Legend != "" {
			y += 1
			y += p.text(margin, y, fullW, doc.Legend, false, 0.85, "L")
		}
		if len(doc.Signatures) > 0 {
			signatures(pdf, p, doc.Signatures, &y)
		}
	}
	return output(pdf)
}

// footer prints on every page when it closes: the page and the total ({nb}), and when it was generated
func footer(pdf *fpdf.Fpdf, p *pen, w, pageHeight float64, issued string) {
	pdf.AliasNbPages("{nb}")
	pdf.SetFooterFunc(func() {
		p.text(margin, pageHeight-margin-3, w, issued+" · ListerCore Edu", false, 0.75, "R")
		p.text(margin, pageHeight-margin-3, w, "Página "+strconv.Itoa(pdf.PageNo())+" de {nb}", false, 0.75, "L")
	})
}
