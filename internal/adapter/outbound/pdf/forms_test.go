package pdf

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"api/internal/core/port"
)

func TestFormAndTable(t *testing.T) {
	r := NewRenderer()
	long := strings.Repeat("Texto largo para forzar el salto de línea y de página. ", 30)
	form := port.FormDoc{Header: port.DocHeader{Dre: "DRE Ayacucho", Ugel: "UGEL Huamanga", Institution: "CEBA Demo", ModularCode: "1234567", Title: "Ficha única de matrícula"},
		IssuedOn: time.Now(), Signatures: []string{"Estudiante", "Dirección"}}
	for i := 0; i < 12; i++ {
		form.Blocks = append(form.Blocks, port.FormBlock{Title: "Bloque", Columns: 2, Fields: []port.FormField{{Label: "Campo", Value: "Valor"}, {Label: "Largo", Value: long}, {Label: "Vacío"}}})
	}
	out, err := r.Form(form)
	if err != nil || !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatalf("form: %v", err)
	}
	form.Signatures = nil
	form.Seal = &port.DocumentSeal{Version: 1, State: "Pendiente de firma", Mark: "PENDIENTE DE FIRMA", VerificationCode: "AAAA-BBBB-CCCC", TrackingCode: "abc",
		TrackingURL: "https://example.test/seguimiento/abc", QR: "https://example.test/seguimiento/abc?codigo=AAAA", Signatures: []port.SealSignature{{Name: "Directora", Position: "Directora"}}}
	if _, err := r.Form(form); err != nil {
		t.Fatalf("form with seal: %v", err)
	}
	table := port.TableDoc{Header: port.DocHeader{Institution: "CEBA Demo", Title: "Nómina de matrícula"}, Columns: []string{"N.°", "Nombre"}, Widths: []float64{1, 9},
		IssuedOn: time.Now(), Signatures: []string{"Secretaría"}}
	g := port.TableGroup{Title: "1.° A", Total: "Total"}
	for i := 0; i < 80; i++ {
		g.Rows = append(g.Rows, []string{"1", "Apellido, Nombre"})
	}
	table.Groups = []port.TableGroup{g, g}
	if out, err = r.Table(table); err != nil || !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatalf("table: %v", err)
	}
	// Two groups of 80 rows: at least four pages, all kept
	if !bytes.Contains(out, []byte("/Count 4")) && !bytes.Contains(out, []byte("/Count 5")) && !bytes.Contains(out, []byte("/Count 6")) {
		t.Fatal("pages of the table lost")
	}
	if _, err := r.Table(port.TableDoc{Header: port.DocHeader{Title: "Vacía"}, Columns: []string{"A"}, Widths: []float64{1}}); err != nil {
		t.Fatalf("empty table: %v", err)
	}
}
