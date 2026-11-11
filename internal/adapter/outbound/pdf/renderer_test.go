package pdf

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"api/internal/core/port"
)

// save writes a sample to PDF_OUT (when set) so it can be looked at
func save(t *testing.T, name string, data []byte) {
	t.Helper()
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		t.Fatalf("%s is not a PDF", name)
	}
	if dir := os.Getenv("PDF_OUT"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func sampleCard() port.ReportCardDoc {
	comp := func(name string, levels ...string) port.CardCompetencyDoc {
		return port.CardCompetencyDoc{Name: name, Levels: levels, Annual: ""}
	}
	conduct := 17
	return port.ReportCardDoc{
		Literal: true, Institution: "Joseph Colegio", Dre: "DRE Ayacucho", Ugel: "UGEL Huamanga", ModularCode: "9999101", Year: "2026",
		Level: "Primaria", Grade: "4.° grado", Section: "A", Student: "Ayala Tito, Valentina Isabella", Code: "2026-0205", Document: "70000770",
		Tutor: "Óscar Rojas Cárdenas", Provisional: true, TermNumber: 3, IssuedOn: time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC),
		Terms: []port.CardTermDoc{{Name: "I", Comment: "Muestra interés y buena disposición en clase.", Justified: 2, Absent: 1, Late: 4, Conduct: &conduct}, {Name: "II", Comment: "Participa con entusiasmo."}, {Name: "III"}, {Name: "IV", Empty: true}},
		Areas: []port.CardAreaDoc{
			{Name: "Matemática", Competencies: []port.CardCompetencyDoc{comp("Resuelve problemas de cantidad", "A", "A", "B", ""), comp("Resuelve problemas de regularidad, equivalencia y cambio", "A", "B", "A", ""), comp("Resuelve problemas de forma, movimiento y localización", "A", "A", "A", ""), comp("Resuelve problemas de gestión de datos e incertidumbre", "B", "A", "A", "")}},
			{Name: "Comunicación", Competencies: []port.CardCompetencyDoc{comp("Se comunica oralmente en su lengua materna", "A", "A", "A", ""), comp("Lee diversos tipos de textos escritos en su lengua materna", "AD", "A", "A", ""), comp("Escribe diversos tipos de textos en su lengua materna", "A", "A", "B", "")}},
			{Name: "Personal Social", Competencies: []port.CardCompetencyDoc{comp("Construye su identidad", "A", "A", "A", ""), comp("Convive y participa democráticamente en la búsqueda del bien común", "A", "A", "A", ""), comp("Construye interpretaciones históricas", "A", "B", "A", ""), comp("Gestiona responsablemente el espacio y el ambiente", "A", "A", "A", ""), comp("Gestiona responsablemente los recursos económicos", "A", "A", "C", "")}},
			{Name: "Ciencia y Tecnología", Competencies: []port.CardCompetencyDoc{comp("Indaga mediante métodos científicos para construir sus conocimientos", "A", "A", "A", ""), comp("Explica el mundo físico basándose en conocimientos sobre los seres vivos, materia y energía, biodiversidad, Tierra y universo", "A", "A", "A", ""), comp("Diseña y construye soluciones tecnológicas para resolver problemas de su entorno", "A", "B", "A", "")}},
			{Name: "Arte y Cultura", Competencies: []port.CardCompetencyDoc{comp("Aprecia de manera crítica manifestaciones artístico-culturales", "A", "A", "A", ""), comp("Crea proyectos desde los lenguajes artísticos", "A", "A", "A", "")}},
			{Name: "Educación Física", Competencies: []port.CardCompetencyDoc{comp("Se desenvuelve de manera autónoma a través de su motricidad", "A", "A", "A", ""), comp("Asume una vida saludable", "A", "A", "A", ""), comp("Interactúa a través de sus habilidades sociomotrices", "A", "A", "A", "")}},
			{Name: "Educación Religiosa", Competencies: []port.CardCompetencyDoc{comp("Construye su identidad como persona humana, amada por Dios, digna, libre y trascendente, comprendiendo la doctrina de su propia religión, abierto al diálogo con las que le son cercanas", "A", "A", "A", "")}},
			{Name: "Competencias transversales", Transversal: true, Competencies: []port.CardCompetencyDoc{comp("Se desenvuelve en entornos virtuales generados por las TIC", "A", "A", "A", ""), comp("Gestiona su aprendizaje de manera autónoma", "A", "A", "A", "")}},
		},
		Conclusions: []port.CardConclusionDoc{{Competency: "Resuelve problemas de cantidad", Text: "Avanza, pero todavía necesita apoyo para resolver problemas con fracciones."}},
	}
}

func TestReportCards(t *testing.T) {
	out, err := NewRenderer().ReportCards([]port.ReportCardDoc{sampleCard()})
	if err != nil {
		t.Fatal(err)
	}
	save(t, "card.pdf", out)
}

func TestSchedulesRecordReceipt(t *testing.T) {
	fill := &port.Colour{R: 214, G: 230, B: 251}
	rows := []port.GridRow{{Head: "07:45\n08:30", Cells: []port.GridCell{{Code: "MAT", Detail: "M. Pari", Fill: fill}, {}, {Code: "COM", Detail: "M. Pari", Fill: fill}, {}, {}}}, {Head: "10:00", Break: true}}
	doc := port.ScheduleDoc{Institution: "Joseph Colegio", Logo: port.Image{Data: escudo, Type: "png"}, IssuedOn: time.Now(), Pages: []port.SchedulePage{{Title: "HORARIO DE CLASES 2026", Subtitle: "2.° grado «A» · Primaria", Info: "Turno mañana",
		Grids:   []port.ScheduleGrid{{Corner: "Hora", Columns: []string{"LUNES", "MARTES", "MIÉRCOLES", "JUEVES", "VIERNES"}, Rows: rows}},
		Legends: []port.LegendTable{{Title: "LEYENDA", Columns: []string{"Sigla", "Curso", "Docente", "Horas"}, Widths: []float64{12, 38, 38, 12}, Rows: []port.LegendRow{{Fills: []*port.Colour{fill}, Cells: []string{"MAT", "Matemática", "Manuel Pari Medina", "5"}}}}}}}}
	out, err := NewRenderer().Schedules(doc)
	if err != nil {
		t.Fatal(err)
	}
	save(t, "schedule.pdf", out)
	// A signed version, with two signers (one still waiting) and a teacher week split in course and section
	signed := time.Date(2026, 10, 31, 22, 11, 0, 0, time.UTC)
	doc.Seal = &port.DocumentSeal{Version: 2, State: "Pendiente de firma", Mark: "PENDIENTE DE FIRMA", VerificationCode: "817E-6AD9-4CFA", TrackingCode: "6hg6je7rwt7k",
		TrackingURL: "https://joseph.pe/seguimiento/6hg6je7rwt7k", QR: "https://joseph.pe/seguimiento/6hg6je7rwt7k?codigo=817E-6AD9-4CFA",
		Signatures: []port.SealSignature{{Name: "Isaías Ramos López", Position: "Responsable de la Oficina de Tecnologías de la Información", SignedAt: &signed, Code: "A1B2-C3D4-E5F6"}, {Name: "Rosa Quispe Mamani", Position: "Directora"}}}
	sub := []port.GridRow{{Head: "07:45\n08:30", Cells: []port.GridCell{{Code: "MAT", Fill: fill}, {Code: "1.° A", Detail: "Secundaria", Fill: fill}, {}, {}, {}, {}, {}, {}, {}, {}}}}
	doc.Pages = append(doc.Pages, port.SchedulePage{Title: "HORARIO DEL DOCENTE 2026", Subtitle: "Manuel Pari Medina",
		Grids: []port.ScheduleGrid{{Corner: "Hora", Columns: []string{"LUNES", "MARTES", "MIÉRCOLES", "JUEVES", "VIERNES"}, SubColumns: []string{"Curso", "Sección"}, Rows: sub}}})
	if out, err = NewRenderer().Schedules(doc); err != nil {
		t.Fatal(err)
	}
	save(t, "schedule-signed.pdf", out)
	out, err = NewRenderer().Record(port.RecordDoc{Institution: "Joseph Colegio", Title: "Acta consolidada de evaluación", Subtitle: "Año lectivo 2026 · 4.° A · Primaria", Code: "ACT-2026-0001", Issued: "11/11/2026",
		Groups: []port.RecordGroupDoc{{Name: "Matemática", Codes: []string{"C1", "C2"}}}, Rows: []port.RecordRowDoc{{N: 1, Code: "2026-0001", Name: "Apaza, Ana", Cells: []string{"A", "B"}, Situation: "PRO"}}, Signatures: []string{"Tutor(a)", "Dirección"}})
	if err != nil {
		t.Fatal(err)
	}
	save(t, "record.pdf", out)
	out, err = NewRenderer().Receipt(port.ReceiptDoc{Institution: "Joseph Colegio", Number: "R001-000123", PaidOn: "11/11/2026", Student: "Ana Apaza (2026-0001)", Method: "Yape", Lines: []port.ReceiptLineDoc{{Concept: "Pensión de noviembre", Amount: "S/ 380.00"}}, Total: "S/ 380.00", ReceivedBy: "Tesorería", Annulled: true, AnnulReason: "Error"})
	if err != nil {
		t.Fatal(err)
	}
	save(t, "receipt.pdf", out)
}
