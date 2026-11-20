// Package pdf draws the printable documents (report cards, timetables, actas, receipts) as PDF with go-pdf/fpdf.
// Everything is drawn by code in millimetres on A4 with the Agency FB typeface embedded, so the server needs no
// browser or system fonts
package pdf

import (
	"bytes"
	_ "embed"
	"fmt"
	"hash/crc32"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"

	"api/internal/core/port"
)

var (
	//go:embed assets/agency-fb-regular.ttf
	fontRegular []byte
	//go:embed assets/agency-fb-bold.ttf
	fontBold []byte
	//go:embed assets/escudo-peru.png
	escudo []byte
)

const family = "agency"

// Renderer implements port.DocumentRenderer
type Renderer struct{}

func NewRenderer() *Renderer { return &Renderer{} }

var _ port.DocumentRenderer = (*Renderer)(nil)

// newDoc starts an A4 document (landscape or portrait) with the fonts loaded and no automatic page breaks: every
// layout decides its own pages
func newDoc(landscape bool, title string) *fpdf.Fpdf {
	orientation := "P"
	if landscape {
		orientation = "L"
	}
	pdf := fpdf.New(orientation, "mm", "A4", "")
	pdf.SetMargins(margin, margin, margin)
	pdf.SetAutoPageBreak(false, 0)
	pdf.AddUTF8FontFromBytes(family, "", fontRegular)
	pdf.AddUTF8FontFromBytes(family, "B", fontBold)
	pdf.SetTitle(title, true)
	pdf.SetCreator("ListerCore Edu", true)
	pdf.SetDrawColor(0, 0, 0)
	pdf.SetTextColor(0, 0, 0)
	pdf.SetLineWidth(0.2)
	return pdf
}

// Page margin in millimetres
const margin = 8.0

// output closes the document into bytes
func output(pdf *fpdf.Fpdf) ([]byte, error) {
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("pdf output: %w", err)
	}
	return buf.Bytes(), nil
}

// image registers a picture once per document and returns its name ("" when it cannot be used)
func image(pdf *fpdf.Fpdf, name string, img port.Image) string {
	if len(img.Data) == 0 {
		return ""
	}
	kind := strings.ToUpper(img.Type)
	if kind == "JPEG" {
		kind = "JPG"
	}
	if kind != "PNG" && kind != "JPG" {
		return ""
	}
	if info := pdf.GetImageInfo(name); info == nil {
		pdf.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: kind}, bytes.NewReader(img.Data))
		if pdf.Err() {
			// A broken logo must not cost the whole document
			pdf.ClearError()
			return ""
		}
	}
	return name
}

// fit draws an image inside a box keeping its proportions, centred
func fit(pdf *fpdf.Fpdf, name string, x, y, w, h float64) {
	if name == "" {
		return
	}
	info := pdf.GetImageInfo(name)
	if info == nil || info.Width() == 0 || info.Height() == 0 {
		return
	}
	ratio := info.Width() / info.Height()
	iw, ih := w, w/ratio
	if ih > h {
		iw, ih = h*ratio, h
	}
	pdf.ImageOptions(name, x+(w-iw)/2, y+(h-ih)/2, iw, ih, false, fpdf.ImageOptions{}, 0, "")
}

// pen holds the font size in points of a layout and draws text boxes; dry runs measure without drawing
type pen struct {
	pdf  *fpdf.Fpdf
	size float64
	dry  bool
}

// lineH is the height of one text line at a size
func lineH(size float64) float64 { return size * 0.3528 * 1.18 }

// font sets the family at a scale of the pen's base size
func (p *pen) font(bold bool, scale float64) {
	style := ""
	if bold {
		style = "B"
	}
	p.pdf.SetFont(family, style, p.size*scale)
}

// wrap splits a text into the lines that fit a width at the current font
func (p *pen) wrap(text string, w float64) []string {
	if text == "" {
		return []string{""}
	}
	var out []string
	for _, para := range strings.Split(text, "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		out = append(out, p.pdf.SplitText(para, w)...)
	}
	return out
}

// box is a table cell: border, optional fill and text wrapped inside, aligned (L, C, R) and centred vertically
type box struct {
	text   string
	bold   bool
	scale  float64
	align  string
	fill   *port.Colour
	border bool
	top    bool
	// One line always: the font shrinks until the text fits (levels, short headers)
	nowrap bool
}

const pad = 0.8

// height of a box at a width
func (p *pen) height(b box, w float64) float64 {
	if b.nowrap {
		return lineH(p.size*scaleOf(b)) + 2*pad
	}
	p.font(b.bold, scaleOf(b))
	return float64(len(p.wrap(b.text, w-2*pad)))*lineH(p.size*scaleOf(b)) + 2*pad
}

// shrunk is the scale at which a one-line text fits a width
func (p *pen) shrunk(b box, w float64) float64 {
	scale := scaleOf(b)
	for scale > 0.4 {
		p.font(b.bold, scale)
		if p.pdf.GetStringWidth(b.text) <= w-2*pad {
			break
		}
		scale -= 0.05
	}
	return scale
}

func scaleOf(b box) float64 {
	if b.scale == 0 {
		return 1
	}
	return b.scale
}

// draw paints a box at a size
func (p *pen) draw(b box, x, y, w, h float64) {
	if p.dry {
		return
	}
	style := ""
	if b.fill != nil {
		p.pdf.SetFillColor(b.fill.R, b.fill.G, b.fill.B)
		style = "F"
	}
	if b.border {
		style += "D"
	}
	if style != "" {
		p.pdf.Rect(x, y, w, h, style)
	}
	if b.text == "" {
		return
	}
	scale := scaleOf(b)
	var lines []string
	if b.nowrap {
		scale = p.shrunk(b, w)
		p.font(b.bold, scale)
		lines = []string{b.text}
	} else {
		p.font(b.bold, scale)
		lines = p.wrap(b.text, w-2*pad)
	}
	lh := lineH(p.size * scale)
	ty := y + (h-float64(len(lines))*lh)/2
	if b.top {
		ty = y + pad
	}
	align := b.align
	if align == "" {
		align = "L"
	}
	for i, l := range lines {
		p.pdf.SetXY(x+pad, ty+float64(i)*lh)
		p.pdf.CellFormat(w-2*pad, lh, l, "", 0, align+"M", false, 0, "")
	}
}

// row draws boxes side by side with the given widths at the height of the tallest one; returns that height
func (p *pen) row(x, y float64, widths []float64, boxes []box, minH float64) float64 {
	h := minH
	for i, b := range boxes {
		if bh := p.height(b, widths[i]); bh > h {
			h = bh
		}
	}
	cx := x
	for i, b := range boxes {
		p.draw(b, cx, y, widths[i], h)
		cx += widths[i]
	}
	return h
}

// text writes a free line (no box) and returns its height
func (p *pen) text(x, y, w float64, s string, bold bool, scale float64, align string) float64 {
	p.font(bold, scale)
	lines := p.wrap(s, w)
	lh := lineH(p.size * scale)
	if !p.dry {
		for i, l := range lines {
			p.pdf.SetXY(x, y+float64(i)*lh)
			p.pdf.CellFormat(w, lh, l, "", 0, align+"M", false, 0, "")
		}
	}
	return float64(len(lines)) * lh
}

// cols turns relative widths into millimetres for a total width
func cols(total float64, rel ...float64) []float64 {
	sum := 0.0
	for _, r := range rel {
		sum += r
	}
	out := make([]float64, len(rel))
	for i, r := range rel {
		out[i] = total * r / sum
	}
	return out
}

var grey = &port.Colour{R: 232, G: 232, B: 232}

// logoName names an image by its content, so the same logo is embedded once however many pages carry it
func logoName(img port.Image) string {
	if len(img.Data) == 0 {
		return ""
	}
	return fmt.Sprintf("logo-%08x", crc32.ChecksumIEEE(img.Data))
}

var (
	weekdays = []string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}
	months   = []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}
)

// longDate writes "miércoles, 11 de noviembre de 2026"
func longDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return fmt.Sprintf("%s, %d de %s de %d", weekdays[t.Weekday()], t.Day(), months[t.Month()-1], t.Year())
}
