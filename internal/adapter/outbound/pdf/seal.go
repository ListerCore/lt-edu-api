package pdf

import (
	"strconv"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/skip2/go-qrcode"

	"api/internal/core/port"
)

// Verification block of an issued document: the signature boxes (with room above each for the official digital
// signature stamp) and, on the right, the QR to the tracking page with the version and its codes

const (
	qrSize      = 22.0
	sealInfoW   = 66.0
	stampRoom   = 10.0
	signaturesN = 3
)

// lima is the time zone the signature dates print in
var lima = func() *time.Location {
	if loc, err := time.LoadLocation("America/Lima"); err == nil {
		return loc
	}
	return time.FixedZone("PET", -5*3600)
}()

// signatureLines are the lines of a signature box, signed or waiting
func signatureLines(s port.SealSignature, preview bool) [][2]string {
	if s.SignedAt != nil {
		return [][2]string{
			{"B", "Firmado digitalmente por: " + s.Name},
			{"", s.Position},
			{"", "Fecha: " + s.SignedAt.In(lima).Format("02/01/2006 15:04") + " · Código de verificación: " + s.Code},
			{"I", "Firma simulada: se reemplazará por la firma digital oficial."},
		}
	}
	state := "Pendiente de firma"
	if preview {
		state = "Firma al publicar"
	}
	return [][2]string{{"B", s.Name}, {"", s.Position}, {"I", state}}
}

// seal draws (or measures) the verification block and returns its height
func seal(p *pen, x, y, w float64, s port.DocumentSeal) float64 {
	preview := s.Version == 0
	sigW := w - qrSize - sealInfoW - 6
	n := len(s.Signatures)
	perRow := min(max(n, 1), signaturesN)
	boxW := (sigW - float64(perRow-1)*4) / float64(perRow)
	lh := lineH(p.size * 0.72)
	sigH := 0.0
	for row := 0; row*perRow < max(n, 1); row++ {
		tallest := 0.0
		for i := row * perRow; i < min(n, (row+1)*perRow); i++ {
			lines := 0
			p.font(false, 0.72)
			for _, l := range signatureLines(s.Signatures[i], preview) {
				lines += len(p.wrap(l[1], boxW))
			}
			tallest = max(tallest, stampRoom+0.8+float64(lines)*lh)
		}
		if !p.dry {
			for i := row * perRow; i < min(n, (row+1)*perRow); i++ {
				bx := x + float64(i-row*perRow)*(boxW+4)
				by := y + sigH
				p.pdf.SetDrawColor(120, 120, 120)
				p.pdf.SetLineWidth(0.25)
				p.pdf.Line(bx, by+stampRoom, bx+boxW, by+stampRoom)
				p.pdf.SetDrawColor(0, 0, 0)
				p.pdf.SetLineWidth(0.2)
				ty := by + stampRoom + 0.8
				for _, l := range signatureLines(s.Signatures[i], preview) {
					p.font(l[0] == "B", 0.72)
					if l[0] == "I" {
						// Agency FB has no italic: notes go smaller and in grey
						p.font(false, 0.68)
						p.pdf.SetTextColor(90, 90, 90)
					}
					for _, t := range p.wrap(l[1], boxW) {
						p.pdf.SetXY(bx, ty)
						p.pdf.CellFormat(boxW, lh, t, "", 0, "LM", false, 0, "")
						ty += lh
					}
					p.pdf.SetTextColor(0, 0, 0)
				}
			}
		}
		sigH += max(tallest, stampRoom+3*lh)
		if (row+1)*perRow < n {
			sigH += 2
		}
	}
	if n == 0 && !p.dry {
		p.text(x, y+stampRoom, sigW, "Sin firmantes asignados", false, 0.72, "L")
	}

	// Codes and QR on the right
	ix := x + w - qrSize - sealInfoW - 2
	info := [][2]string{}
	if preview {
		info = append(info, [2]string{"B", "Vista previa sin validez"}, [2]string{"", "El QR y los códigos se asignan al generar la versión y la publicación llega con las firmas."})
	} else {
		info = append(info,
			[2]string{"B", "Versión " + strconv.Itoa(s.Version) + " · " + s.State},
			[2]string{"", "Código de verificación: " + s.VerificationCode},
			[2]string{"", "Seguimiento: " + s.TrackingURL},
			[2]string{"", "Escanee el QR para comprobar si esta es la versión vigente."})
	}
	infoH := 0.0
	for _, l := range info {
		p.font(l[0] == "B", 0.72)
		for _, t := range p.wrap(l[1], sealInfoW) {
			if !p.dry {
				p.pdf.SetXY(ix, y+infoH)
				p.pdf.CellFormat(sealInfoW, lh, t, "", 0, "RM", false, 0, "")
			}
			infoH += lh
		}
	}
	qx := x + w - qrSize
	if !p.dry {
		if preview || s.QR == "" {
			p.pdf.SetDrawColor(150, 150, 150)
			p.pdf.SetDashPattern([]float64{1, 1}, 0)
			p.pdf.Rect(qx, y, qrSize, qrSize, "D")
			p.pdf.SetDashPattern([]float64{}, 0)
			p.pdf.SetDrawColor(0, 0, 0)
			p.font(false, 0.7)
			p.pdf.SetXY(qx, y)
			p.pdf.CellFormat(qrSize, qrSize, "QR", "", 0, "CM", false, 0, "")
		} else {
			drawQR(p.pdf, s.QR, qx, y, qrSize)
		}
	}
	return max(sigH, infoH, qrSize)
}

// drawQR paints a QR code as vector squares, merging the dark runs of each row
func drawQR(pdf *fpdf.Fpdf, content string, x, y, size float64) {
	q, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return
	}
	q.DisableBorder = true
	bits := q.Bitmap()
	if len(bits) == 0 {
		return
	}
	cell := size / float64(len(bits))
	pdf.SetFillColor(0, 0, 0)
	for r, row := range bits {
		for c := 0; c < len(row); c++ {
			if !row[c] {
				continue
			}
			end := c
			for end+1 < len(row) && row[end+1] {
				end++
			}
			// A hair of overlap so no seams show between modules
			pdf.Rect(x+float64(c)*cell, y+float64(r)*cell, float64(end-c+1)*cell+0.02, cell+0.02, "F")
			c = end
		}
	}
}

// watermark crosses a sheet with a large light text (VISTA PREVIA, PENDIENTE DE FIRMA…)
func watermark(pdf *fpdf.Fpdf, text string, w, h float64) {
	if text == "" {
		return
	}
	pdf.SetTextColor(200, 40, 60)
	pdf.SetAlpha(0.13, "Normal")
	size := min(90, 1500/float64(max(len([]rune(text)), 1)))
	pdf.SetFont(family, "B", size)
	pdf.TransformBegin()
	pdf.TransformRotate(24, w/2, h/2)
	pdf.SetXY(0, h/2-15)
	pdf.CellFormat(w, 30, text, "", 0, "CM", false, 0, "")
	pdf.TransformEnd()
	pdf.SetAlpha(1, "Normal")
	pdf.SetTextColor(0, 0, 0)
}
