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

// A signed acta: official heading, grade column of a multigrade section, EXO cells and the seal on the last page
func TestSignedRecord(t *testing.T) {
	doc := port.RecordDoc{Institution: "CEBA Demo", Title: "Acta consolidada de evaluación integral", Code: "AF-2026-001", Issued: "15/12/2026",
		Header: &port.DocHeader{Dre: "DRE Ayacucho", Ugel: "UGEL Huamanga", Institution: "CEBA Demo", ModularCode: "1234567", Title: "Acta consolidada", Code: "Acta AF-2026-001"},
		Groups: []port.RecordGroupDoc{{Name: "Matemática", Codes: []string{"1.1", "1.2"}}, {Name: "Educación Religiosa", Codes: []string{"2.1", "2.2"}}},
		Seal: &port.DocumentSeal{Version: 1, State: "Pendiente de firma", Mark: "PENDIENTE DE FIRMA", VerificationCode: "AAAA-BBBB-CCCC", TrackingCode: "abc",
			TrackingURL: "https://example.test/seguimiento/abc", QR: "https://example.test/seguimiento/abc?codigo=AAAA", Signatures: []port.SealSignature{{Name: "Directora", Position: "Directora"}}}}
	for i := 0; i < 60; i++ {
		doc.Rows = append(doc.Rows, port.RecordRowDoc{N: i + 1, Code: "E-1", Grade: "3.°", Name: "Apellido, Nombre", Cells: []string{"A", "B", "EXO", "EXO"}, Situation: "Promovido"})
	}
	out, err := NewRenderer().Record(doc)
	if err != nil || !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatalf("signed record: %v", err)
	}
	if !bytes.Contains(out, []byte("/Count 3")) && !bytes.Contains(out, []byte("/Count 4")) {
		t.Fatalf("60 rows should take three or four pages")
	}
}

// A technical report: running header, matrices across pages, signature and annex pages (a broken image does not fail)
func TestTechReport(t *testing.T) {
	doc := port.TechReportDoc{RunningHeader: "CEBA DEMO | DOCUMENTACIÓN TÉCNICA", RunningSub: "Expediente N.° 2026-0013636 • Ayacucho", Number: "INFORME TÉCNICO N.° 001-2026",
		Title: "INFORME TÉCNICO DE IMPLEMENTACIÓN DE LA PLATAFORMA", Place: "Ayacucho, 16 de noviembre de 2026.", Signer: []string{"Firma del responsable técnico", "Nombres y apellidos: Ana Rojas"},
		Draft: true, IssuedOn: time.Now()}
	rows := [][]string{}
	for i := 0; i < 40; i++ {
		rows = append(rows, []string{"Elemento con un nombre largo que ocupa dos líneas", "Módulo › Submódulo\n/dashboard/ruta", "Anexo N.° 03 – Folio 12", "VERIFICADO"})
	}
	doc.Sections = []port.ReportSection{
		{Heading: "I. OBJETO", Paragraphs: []string{strings.Repeat("Texto del objeto del informe. ", 40)}},
		{Heading: "V. SECCIONES", Table: &port.ReportTable{Columns: []string{"ELEMENTO", "MÓDULO / RUTA", "EVIDENCIA", "ESTADO"}, Widths: []float64{30, 34, 20, 16}, Rows: rows, StatusColumn: 3}},
	}
	doc.Annexes = []port.ReportAnnex{{Title: "Anexo 03 · Folio 12 · Ficha", Caption: "Matrícula", Image: port.Image{Data: []byte("not an image"), Type: "png"}}}
	out, err := NewRenderer().TechReport(doc)
	if err != nil || !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatalf("tech report: %v", err)
	}
	if bytes.Contains(out, []byte("/Count 1\n")) || bytes.Contains(out, []byte("/Count 1 ")) {
		t.Fatal("40 rows and an annex need several pages")
	}
}
