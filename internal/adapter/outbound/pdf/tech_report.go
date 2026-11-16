package pdf

import (
	"strconv"

	"github.com/go-pdf/fpdf"

	"api/internal/core/port"
)

// Technical reports for the DRE (phase E8): A4 portrait with wide margins like the templates of the CEBA, the
// running header on every page, numbered sections with their matrices, the signature block and the evidence as
// annex pages, one image per page

const (
	reportMargin = 20.0
	reportW      = portraitW - 2*reportMargin
	reportTop    = 26.0
	reportBottom = portraitH - 18.0
)

func (r *Renderer) TechReport(doc port.TechReportDoc) ([]byte, error) {
	pdf := newDoc(false, doc.Title)
	p := &pen{pdf: pdf, size: 10.5}
	pdf.SetHeaderFunc(func() {
		p.text(reportMargin, 9, reportW, doc.RunningHeader, true, 0.85, "C")
		p.text(reportMargin, 13.2, reportW, doc.RunningSub, false, 0.8, "C")
		pdf.SetLineWidth(0.3)
		pdf.Line(reportMargin, 18, reportMargin+reportW, 18)
		pdf.SetLineWidth(0.2)
		if doc.Draft {
			watermark(pdf, "BORRADOR", portraitW, portraitH)
		}
	})
	pdf.AliasNbPages("{nb}")
	pdf.SetFooterFunc(func() {
		p.text(reportMargin, portraitH-12, reportW, "Página "+strconv.Itoa(pdf.PageNo())+" de {nb}", false, 0.75, "C")
	})
	pdf.AddPage()
	y := reportTop
	need := func(h float64) {
		if y+h > reportBottom {
			pdf.AddPage()
			y = reportTop
		}
	}
	dry := func() *pen { return &pen{pdf: pdf, size: p.size, dry: true} }
	para := func(text string, bold bool, scale float64, align string) {
		h := dry().text(reportMargin, 0, reportW, text, bold, scale, align)
		need(h)
		y += p.text(reportMargin, y, reportW, text, bold, scale, align) + 1.6
	}
	if doc.Number != "" {
		para(doc.Number, true, 1.05, "C")
	}
	para(doc.Title, true, 1.25, "C")
	if doc.Subtitle != "" {
		para(doc.Subtitle, false, 1, "C")
	}
	y += 2
	for _, s := range doc.Sections {
		if s.Heading != "" {
			need(14)
			y += 1.5
			para(s.Heading, true, 1.05, "L")
		}
		for _, t := range s.Paragraphs {
			para(t, false, 1, "L")
		}
		if s.Table != nil {
			y = table(pdf, p, *s.Table, y)
			y += 2
		}
		for _, t := range s.After {
			para(t, false, 1, "L")
		}
	}
	if doc.Place != "" {
		y += 2
		para(doc.Place, false, 1, "R")
	}
	if len(doc.Signer) > 0 {
		need(30)
		y += 16
		pdf.Line(reportMargin+reportW/2-40, y, reportMargin+reportW/2+40, y)
		y += 1
		for _, l := range doc.Signer {
			y += p.text(reportMargin, y, reportW, l, false, 0.95, "C")
		}
	}
	for i, a := range doc.Annexes {
		pdf.AddPage()
		y = reportTop
		y += p.text(reportMargin, y, reportW, a.Title, true, 1.1, "L") + 1
		if a.Caption != "" {
			y += p.text(reportMargin, y, reportW, a.Caption, false, 0.95, "L") + 2
		}
		name := image(pdf, "annex-"+strconv.Itoa(i), a.Image)
		if name == "" {
			p.text(reportMargin, y, reportW, "(La evidencia no es una imagen: se adjunta por separado.)", false, 0.9, "L")
			continue
		}
		// At the top of the page, as wide as it fits
		if info := pdf.GetImageInfo(name); info != nil && info.Width() > 0 {
			w := reportW
			h := w * info.Height() / info.Width()
			if h > reportBottom-y {
				h = reportBottom - y
				w = h * info.Width() / info.Height()
			}
			pdf.ImageOptions(name, reportMargin+(reportW-w)/2, y, w, h, false, fpdf.ImageOptions{}, 0, "")
		}
	}
	return output(pdf)
}

// table draws a matrix with its header repeated on each new page; returns where it ends
func table(pdf *fpdf.Fpdf, p *pen, t port.ReportTable, y float64) float64 {
	widths := cols(reportW, t.Widths...)
	header := make([]box, len(t.Columns))
	for i, c := range t.Columns {
		header[i] = box{text: c, bold: true, fill: grey, border: true, align: "C", scale: 0.85}
	}
	size := p.size
	p.size = 9
	defer func() { p.size = size }()
	measure := &pen{pdf: pdf, size: p.size, dry: true}
	hh := measure.row(0, 0, widths, header, 0)
	if y+hh+8 > reportBottom {
		pdf.AddPage()
		y = reportTop
	}
	y += p.row(reportMargin, y, widths, header, 0)
	for _, row := range t.Rows {
		boxes := make([]box, len(row))
		for i, c := range row {
			boxes[i] = box{text: c, border: true, bold: i == t.StatusColumn && t.StatusColumn >= 0, align: map[bool]string{true: "C", false: "L"}[i == t.StatusColumn]}
		}
		rh := measure.row(0, 0, widths, boxes, 0)
		if y+rh > reportBottom {
			pdf.AddPage()
			y = reportTop
			y += p.row(reportMargin, y, widths, header, 0)
		}
		y += p.row(reportMargin, y, widths, boxes, rh)
	}
	return y
}
