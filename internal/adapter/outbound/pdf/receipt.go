package pdf

import (
	"github.com/go-pdf/fpdf"

	"api/internal/core/port"
)

// Receipt draws the internal payment receipt on A5 portrait; an annulled one carries a large mark across
func (r *Renderer) Receipt(doc port.ReceiptDoc) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A5", "")
	pdf.SetMargins(margin, margin, margin)
	pdf.SetAutoPageBreak(false, 0)
	pdf.AddUTF8FontFromBytes(family, "", fontRegular)
	pdf.AddUTF8FontFromBytes(family, "B", fontBold)
	pdf.SetTitle("Recibo "+doc.Number, true)
	pdf.SetCreator("joseph", true)
	pdf.SetLineWidth(0.2)
	pdf.AddPage()
	const w = 148 - 2*margin
	p := &pen{pdf: pdf, size: 11}
	x := margin
	logo := image(pdf, logoName(doc.Logo), doc.Logo)
	textX := x
	if logo != "" {
		fit(pdf, logo, x, margin, 16, 16)
		textX = x + 19
	}
	y := margin + p.text(textX, margin, w-(textX-x)-45, doc.Institution, true, 1.2, "L")
	p.text(textX, y, w-(textX-x)-45, "Recibo de pago", false, 0.95, "L")
	ny := margin + p.text(x+w-45, margin, 45, doc.Number, true, 1.4, "R")
	p.text(x+w-45, ny, 45, doc.PaidOn, false, 0.95, "R")
	y = margin + 18
	pdf.SetLineWidth(0.6)
	pdf.Line(x, y, x+w, y)
	pdf.SetLineWidth(0.2)
	y += 3
	iw := cols(w, 24, 76)
	rows := [][2]string{{"Estudiante", doc.Student}}
	if doc.Payer != "" {
		rows = append(rows, [2]string{"Pagó", doc.Payer})
	}
	rows = append(rows, [2]string{"Medio", doc.Method})
	for _, kv := range rows {
		y += p.row(x, y, iw, []box{{text: kv[0], scale: 0.95}, {text: kv[1], bold: true}}, 0)
	}
	y += 3
	lw := cols(w, 72, 28)
	y += p.row(x, y, lw, []box{{text: "Concepto", bold: true, border: true, fill: grey}, {text: "Importe", bold: true, align: "R", border: true, fill: grey}}, 0)
	for _, l := range doc.Lines {
		y += p.row(x, y, lw, []box{{text: l.Concept, border: true}, {text: l.Amount, align: "R", border: true}}, 0)
	}
	y += p.row(x, y, lw, []box{{text: "Total", bold: true, scale: 1.1, border: true}, {text: doc.Total, bold: true, scale: 1.25, align: "R", border: true}}, 0)
	y += 4
	if doc.Annulled {
		y += p.text(x, y, w, "Anulado: "+doc.AnnulReason, true, 1, "L") + 2
	}
	p.text(x, y, w, "Recibió: "+doc.ReceivedBy+". Recibo interno de la institución; no reemplaza al comprobante electrónico de SUNAT.", false, 0.85, "L")
	if doc.Annulled {
		pdf.SetTextColor(200, 40, 60)
		pdf.SetAlpha(0.25, "Normal")
		pdf.SetFont(family, "B", 64)
		pdf.TransformBegin()
		pdf.TransformRotate(30, 74, 105)
		pdf.SetXY(0, 95)
		pdf.CellFormat(148, 20, "ANULADO", "", 0, "CM", false, 0, "")
		pdf.TransformEnd()
		pdf.SetAlpha(1, "Normal")
		pdf.SetTextColor(0, 0, 0)
	}
	return output(pdf)
}
