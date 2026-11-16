package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// The two technical reports for the DRE (RSG 613-2014-MINEDU), built from the platform sheet, the verification
// matrices, the equipment and the evidence, following the CEBA's templates (docs/ceba/*.pdf). A report with a row
// still pending prints as a draft

var monthNames = []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

func longDay(t time.Time) string {
	return fmt.Sprintf("%d de %s de %d", t.Day(), monthNames[t.Month()-1], t.Year())
}

func orBlank(v string) string {
	if strings.TrimSpace(v) == "" {
		return "________________________________"
	}
	return v
}

var itemStatusLabels = map[string]string{domain.ItemPending: "PENDIENTE", domain.ItemVerified: "VERIFICADO", domain.ItemNotApplicable: "NO APLICA"}

// statusLabel is the state a row prints: a verified row whose evidence was removed is pending again
func statusLabel(it domain.ComplianceItem) string {
	if it.Status == domain.ItemVerified && it.Files == 0 {
		return itemStatusLabels[domain.ItemPending]
	}
	return itemStatusLabels[it.Status]
}

func evidenceRef(it domain.ComplianceItem) string {
	switch {
	case it.Annex != "" && it.Folio != "":
		return "Anexo N.° " + it.Annex + " – Folio " + it.Folio
	case it.Annex != "":
		return "Anexo N.° " + it.Annex
	case it.Files > 0:
		return fmt.Sprintf("%d %s", it.Files, plural(it.Files, "captura", "capturas"))
	}
	return "—"
}

// reportFrame is what both reports share: who, where, which file, and the evaluation date
type reportFrame struct {
	sc     complianceScope
	report domain.ComplianceReport
	sheet  domain.PlatformSheet
	items  []domain.ComplianceItem
	when   time.Time
}

func (s *ComplianceService) frame(ctx context.Context, token, slug, kind string) (reportFrame, error) {
	sc, err := s.scope(ctx, token, slug, false)
	if err != nil {
		return reportFrame{}, err
	}
	inst := sc.institution.ID
	if err := s.repo.EnsureItems(ctx, inst, defaultItems); err != nil {
		return reportFrame{}, err
	}
	f := reportFrame{sc: sc, when: s.now().In(lima())}
	if f.report, err = s.report(ctx, inst, kind); err != nil {
		return f, err
	}
	if f.sheet, err = s.institution.PlatformSheet(ctx, inst); err != nil {
		return f, err
	}
	if f.items, err = s.repo.Items(ctx, inst, kind); err != nil {
		return f, err
	}
	return f, nil
}

func (f reportFrame) doc(title, subtitle string) port.TechReportDoc {
	name := f.sc.institution.Name
	sub := []string{}
	if f.report.FileNumber != "" {
		sub = append(sub, "Expediente N.° "+f.report.FileNumber)
	}
	if f.report.City != "" {
		sub = append(sub, f.report.City)
	}
	evaluated := "____ / ____ / " + fmt.Sprint(f.when.Year())
	if f.report.EvaluatedOn != nil {
		evaluated = f.report.EvaluatedOn.Format("02/01/2006")
	}
	_ = evaluated
	city := f.report.City
	if city == "" {
		city = "Ayacucho"
	}
	signer := []string{"Firma del responsable técnico", "Nombres y apellidos: " + orBlank(f.sheet.TechnicalLead), "Especialidad: " + orBlank(f.sheet.TechnicalLeadSpecialty)}
	if f.sheet.TechnicalLeadCIP != "" {
		signer = append(signer, "CIP: "+f.sheet.TechnicalLeadCIP)
	}
	return port.TechReportDoc{RunningHeader: strings.ToUpper(name) + " | DOCUMENTACIÓN TÉCNICA", RunningSub: strings.Join(sub, " • "), Title: title, Subtitle: subtitle,
		Place: city + ", " + longDay(f.when) + ".", Signer: signer, Draft: !itemsDone(f.items), IssuedOn: f.when}
}

func (f reportFrame) evaluatedOn() string {
	if f.report.EvaluatedOn != nil {
		return f.report.EvaluatedOn.Format("02/01/2006")
	}
	return "____ / ____ / " + fmt.Sprint(f.when.Year())
}

func pdfName(kind string, t time.Time) string {
	return fmt.Sprintf("informe-tecnico-%s-%s.pdf", kind, t.Format("2006-01-02"))
}

// ---------- 6.2.1 and Anexo 2: the platform ----------

var hostingNames = map[string]string{"hosting": "servicio hosting", "own_server": "servidor propio", "other": "otra modalidad"}

func (s *ComplianceService) PlatformReportPdf(ctx context.Context, token, slug string) (PdfFile, error) {
	f, err := s.frame(ctx, token, slug, domain.ReportPlatform)
	if err != nil {
		return PdfFile{}, err
	}
	inst := f.sc.institution
	sh := f.sheet
	upper := strings.ToUpper(inst.Name)
	doc := f.doc("INFORME TÉCNICO DE IMPLEMENTACIÓN, DISPONIBILIDAD Y FUNCIONAMIENTO DE LA PLATAFORMA EDUCATIVA VIRTUAL (E-LEARNING) DEL "+upper, "")
	number := f.report.Number
	if number == "" {
		number = "____-" + fmt.Sprint(f.when.Year())
	}
	doc.Number = "INFORME TÉCNICO N.° " + number

	uptime, err := s.repo.Uptime(ctx, inst.ID, f.when.AddDate(0, 0, -30))
	if err != nil {
		return PdfFile{}, err
	}
	up := summarize(sh.URL, uptime)
	method := "Comprobación de acceso mediante navegador"
	if up.Checks > 0 {
		method = fmt.Sprintf("Monitoreo automático de la URL: %d comprobaciones en los últimos 30 días, %.1f %% con respuesta correcta, tiempo medio de respuesta %d ms",
			up.Checks, up.Percent, up.AvgMs)
		if up.Last != nil {
			method += "; última comprobación el " + up.Last.CheckedAt.In(lima()).Format("02/01/2006 15:04")
		}
	}
	validity := sh.ServiceStatus
	if sh.ValidUntil != nil {
		validity = strings.TrimSpace(validity + " (vigente hasta el " + sh.ValidUntil.Format("02/01/2006") + ")")
	}
	hosting := hostingNames[sh.HostingKind]
	if sh.HostingKind == "other" && sh.HostingOther != "" {
		hosting = sh.HostingOther
	}
	platformFiles, err := s.files.List(ctx, inst.ID, domain.OwnerPlatformSheet, inst.ID)
	if err != nil {
		return PdfFile{}, err
	}

	row := func(section string) [][]string {
		var out [][]string
		for _, it := range f.items {
			if it.Section == section {
				out = append(out, []string{it.Element, strings.Join(nonBlank(it.Module, it.Route), "\n"), evidenceRef(it), statusLabel(it)})
			}
		}
		return out
	}
	matrix := func(section string) *port.ReportTable {
		return &port.ReportTable{Columns: []string{"ELEMENTO DEL ANEXO 2", "MÓDULO / RUTA REAL", "EVIDENCIA (ANEXO/FOLIO)", "ESTADO"}, Widths: []float64{30, 34, 20, 16},
			Rows: row(section), StatusColumn: 3}
	}
	general := f.report.GeneralResult
	if general == "" {
		general = orBlank("")
	}
	limitations := f.report.Limitations
	if limitations == "" {
		limitations = "ninguna"
	}
	doc.Sections = []port.ReportSection{
		{Table: &port.ReportTable{Columns: []string{"DATOS GENERALES", "INFORMACIÓN"}, Widths: []float64{35, 65}, StatusColumn: -1, Rows: [][]string{
			{"Institución", inst.Name}, {"Ubicación", orBlank(f.report.Location)}, {"Plataforma / dirección URL", strings.Join(nonBlank(sh.PlatformName, sh.URL), " · ")},
			{"Responsable técnico", orBlank(sh.TechnicalLead)}, {"Fecha de evaluación", f.evaluatedOn()},
		}}},
		{Heading: "I. OBJETO", Paragraphs: []string{"El presente informe tiene por objeto describir técnicamente la plataforma educativa virtual (e-learning) del " + inst.Name +
			", identificar la infraestructura de alojamiento que soporta su funcionamiento y documentar los mecanismos de gestión y atención educativa a distancia, a fin de acreditar sus características y disponibilidad ante la Dirección Regional de Educación."}},
		{Heading: "II. MARCO DE REFERENCIA", Paragraphs: []string{"El informe toma como referencia la Resolución de Secretaría General N.° 613-2014-MINEDU, en particular el numeral 6.2.1, relativo a la administración de una plataforma e-learning y la disponibilidad de un servidor o servicio hosting, y el Anexo 2, «Secciones de la plataforma e-learning». También considera el numeral 6.3.2, literal c), en lo concerniente a infraestructura y equipos informáticos para tutores virtuales."}},
		{Heading: "III. IDENTIFICACIÓN Y DESCRIPCIÓN DE LA PLATAFORMA", Paragraphs: []string{
			fmt.Sprintf("La plataforma institucional se identifica como %s, accesible mediante la dirección electrónica %s. Su implementación tecnológica corresponde a %s y la administración técnica se encuentra a cargo de %s.",
				orBlank(sh.PlatformName), orBlank(sh.URL), orBlank(sh.Technology), orBlank(sh.Administrator)),
			"El entorno e-learning está destinado a organizar la información académica y administrativa de la modalidad de atención a distancia, así como a proporcionar espacios virtuales para la interacción entre tutores y estudiantes. Los perfiles de acceso habilitados son: " + orBlank(sh.AccessProfiles) + ". Las funciones descritas corresponden a los módulos efectivamente disponibles a la fecha de verificación.",
		}},
		{Heading: "IV. INFRAESTRUCTURA DE ALOJAMIENTO Y DISPONIBILIDAD", Paragraphs: []string{
			fmt.Sprintf("La plataforma se encuentra alojada bajo la modalidad de %s. El proveedor o responsable del alojamiento es %s y la denominación del servicio, instancia o servidor corresponde a %s.",
				orBlank(hosting), orBlank(sh.Provider), orBlank(sh.Instance)),
		}, Table: &port.ReportTable{Columns: []string{"CARACTERÍSTICA", "DATO TÉCNICO VERIFICADO"}, Widths: []float64{35, 65}, StatusColumn: -1, Rows: [][]string{
			{"Dominio y URL", orBlank(sh.URL)}, {"Proveedor de hosting / servidor", orBlank(sh.Provider)}, {"Identificación del contrato, panel o instancia", orBlank(strings.Join(nonBlank(sh.Instance, sh.ContractReference), " · "))},
			{"Vigencia / estado del servicio", orBlank(validity)}, {"Fecha y método de comprobación de acceso", method},
			{"Sustento anexo", fmt.Sprintf("Anexo N.° 02: %d %s del alojamiento", len(platformFiles), plural(len(platformFiles), "documento o captura", "documentos o capturas"))},
		}}, After: []string{"La disponibilidad del alojamiento se sustenta mediante los documentos y comprobaciones incorporados en los anexos. La información descrita corresponde exclusivamente a las condiciones verificadas en la fecha indicada; no constituye garantía de funcionamiento ininterrumpido."}},
		{Heading: "V. SECCIONES Y FUNCIONALIDADES CONFORME AL ANEXO 2", Paragraphs: []string{
			"La evaluación funcional se presenta de acuerdo con las dos secciones previstas en el Anexo 2 de la RSG N.° 613-2014-MINEDU. Para cada elemento se identifica el módulo real y la evidencia que permite constatar su funcionamiento. La calificación «verificado» corresponde únicamente a elementos comprobados por el responsable técnico.",
			"5.1. Sección informativa. Comprende la base de datos y los reportes de la información automatizada vinculada a matrícula, asistencia o permanencia, participación y proceso de evaluación de aprendizajes.",
		}, Table: matrix("informative")},
		{Paragraphs: []string{"5.2. Sección de aula virtual. Comprende el material educativo estructurado y dosificado por períodos promocionales, campos del conocimiento y grados, así como material complementario, evaluaciones, foros y recursos educativos digitales."},
			Table: matrix("classroom")},
		{Heading: "VI. PROCEDIMIENTO Y RESULTADOS DE VERIFICACIÓN", Paragraphs: []string{
			"La verificación técnica se efectuó el " + f.evaluatedOn() + ", utilizando perfiles de prueba autorizados y recorriendo los módulos consignados en las matrices precedentes. Se comprobó, según corresponda, el acceso mediante navegador, la visualización de los recursos, el registro o consulta de datos de prueba y la generación de reportes. Los resultados por cada función se indican en la columna «Estado» de las matrices, y las capturas de pantalla asociadas se incorporan en el anexo de evidencias.",
			"Resultado general de la verificación: " + general + ". Limitaciones o componentes pendientes, si los hubiera: " + limitations + ".",
		}},
		{Heading: "VII. CONCLUSIONES", Paragraphs: []string{
			"7.1. La plataforma educativa virtual del " + inst.Name + " se identifica en el apartado III, y su infraestructura de alojamiento se documenta en el apartado IV, con los medios probatorios individualizados en los anexos.",
			"7.2. La correspondencia de sus funcionalidades con las secciones informativa y de aula virtual del Anexo 2 de la RSG N.° 613-2014-MINEDU se registra en las matrices del apartado V; el estado de cada componente se determina con base en la verificación técnica y sus respectivas evidencias.",
			"7.3. Sobre la base de los resultados consignados, el responsable técnico concluye lo siguiente respecto de la implementación y funcionamiento observado: " + orBlank(f.report.Conclusion) + ".",
		}},
		{Heading: "VIII. DOCUMENTACIÓN ANEXA", Paragraphs: []string{
			"Anexo 01. Evidencia de la identificación y acceso a la plataforma (URL y pantalla de ingreso).",
			"Anexo 02. Documentación verificable del hosting o servidor (constancia, contrato o panel de servicio, con información sensible protegida).",
			"Anexo 03. Capturas y pruebas de los cinco elementos de la sección informativa, numeradas y con folio.",
			"Anexo 04. Capturas y pruebas de los componentes de la sección de aula virtual, numeradas y con folio.",
			"Se deja constancia de que las evidencias adjuntas corresponden a funcionalidades verificadas, utilizando información de prueba o datos personales debidamente anonimizados.",
		}},
	}
	// Annexes: the hosting papers, then the captures of each row in order
	hostingImages, err := s.evidence(ctx, inst.ID, domain.OwnerPlatformSheet, inst.ID)
	if err != nil {
		return PdfFile{}, err
	}
	for i, img := range hostingImages {
		doc.Annexes = append(doc.Annexes, port.ReportAnnex{Title: fmt.Sprintf("Anexo 02 · Documento %d", i+1), Caption: "Sustento del alojamiento: " + strings.Join(nonBlank(sh.Provider, sh.Instance), " · "), Image: img})
	}
	if err := s.itemAnnexes(ctx, inst.ID, f.items, map[string]string{"informative": "03", "classroom": "04"}, &doc); err != nil {
		return PdfFile{}, err
	}
	content, err := s.render.TechReport(doc)
	if err != nil {
		return PdfFile{}, err
	}
	return PdfFile{Name: pdfName("plataforma", f.when), Content: content}, nil
}

// itemAnnexes adds the captures of each row as annex pages (annex number per section, unless the row says its own)
func (s *ComplianceService) itemAnnexes(ctx context.Context, institutionID string, items []domain.ComplianceItem, annexOf map[string]string, doc *port.TechReportDoc) error {
	for _, it := range items {
		if it.Files == 0 || it.Status == domain.ItemNotApplicable {
			continue
		}
		images, err := s.evidence(ctx, institutionID, domain.OwnerComplianceItem, it.ID)
		if err != nil {
			return err
		}
		annex := it.Annex
		if annex == "" {
			annex = annexOf[it.Section]
		}
		for i, img := range images {
			title := "Anexo " + annex
			if it.Folio != "" {
				title += " · Folio " + it.Folio
			}
			title += " · " + it.Element
			if len(images) > 1 {
				title += fmt.Sprintf(" (%d de %d)", i+1, len(images))
			}
			doc.Annexes = append(doc.Annexes, port.ReportAnnex{Title: title, Caption: strings.Join(nonBlank(it.Module, it.Route), " · "), Image: img})
		}
	}
	return nil
}

// ---------- 6.3.2 c: the virtual tutors' equipment ----------

var (
	equipmentKindNames = map[string]string{"desktop": "Computadora", "laptop": "Laptop", "tablet": "Tableta", "network": "Equipo de red", "printer": "Impresora", "other": "Otro equipo"}
	purposeNames       = map[string]string{"virtual_tutoring": "Tutoría virtual", "student_support": "Soporte a estudiantes", "administration": "Administración", "shared": "Compartido"}
	linkNames          = map[string]string{"fiber": "fibra óptica", "adsl": "ADSL", "wireless": "inalámbrica", "mobile": "móvil", "other": "otra"}
	resultNames        = map[string]string{domain.ItemPending: "Pendiente", domain.ItemVerified: "Verificado", domain.ItemNotApplicable: "No aplica"}
)

func (s *ComplianceService) EquipmentReportPdf(ctx context.Context, token, slug string) (PdfFile, error) {
	f, err := s.frame(ctx, token, slug, domain.ReportEquipment)
	if err != nil {
		return PdfFile{}, err
	}
	inst := f.sc.institution
	rooms, err := s.repo.Rooms(ctx, inst.ID)
	if err != nil {
		return PdfFile{}, err
	}
	devices, err := s.repo.Devices(ctx, inst.ID)
	if err != nil {
		return PdfFile{}, err
	}
	checks, err := s.repo.Checks(ctx, inst.ID, 200)
	if err != nil {
		return PdfFile{}, err
	}
	doc := f.doc("INFORME TÉCNICO DE DISPONIBILIDAD, ASIGNACIÓN Y USO DE EQUIPOS INFORMÁTICOS PARA TUTORES VIRTUALES", inst.Name)
	verifier := strings.Join(nonBlank(f.sheet.TechnicalLead, f.sheet.TechnicalLeadSpecialty), " – ")

	// III: each environment with its latest connectivity test
	var spaces []string
	for _, r := range rooms {
		t := fmt.Sprintf("En el local institucional se identifica el ambiente denominado «%s»", r.Name)
		if r.Location != "" {
			t += ", situado en " + lowerFirst(r.Location)
		}
		t += "."
		if r.AreaM2 != nil {
			t += fmt.Sprintf(" Tiene un área aproximada de %.1f m²", *r.AreaM2)
		} else {
			t += " Su área está por consignar"
		}
		t += fmt.Sprintf(" y dispone de %d %s de trabajo para el desarrollo de actividades de tutoría virtual.", r.Workstations, plural(r.Workstations, "puesto", "puestos"))
		if r.Furniture != "" || r.PowerSupply != "" {
			t += " El ambiente dispone de " + strings.Join(nonBlank(r.Furniture, r.PowerSupply), "; ") + "."
		}
		for _, c := range checks {
			if c.Kind == "connectivity" && c.RoomID == r.ID {
				t += fmt.Sprintf(" La disponibilidad de conexión a internet se constató mediante prueba de conexión (%s) el %s.", strings.Join(nonBlank(c.Provider, linkNames[c.LinkKind]), ", "), c.CheckedOn.Format("02/01/2006"))
				break
			}
		}
		if r.Files > 0 {
			t += fmt.Sprintf(" Su distribución y equipamiento se representan en el registro fotográfico del Anexo 01 (%d %s).", r.Files, plural(r.Files, "imagen", "imágenes"))
		}
		spaces = append(spaces, t)
	}
	if len(spaces) == 0 {
		spaces = []string{"No se ha registrado aún el ambiente destinado a la tutoría virtual."}
	}

	// IV: the inventory
	var inventory [][]string
	counts := map[string]int{}
	for _, d := range devices {
		desc := strings.Join(nonBlank(equipmentKindNames[d.Kind], strings.Join(nonBlank(d.Brand, d.Model), " "), d.Specs), "; ")
		user := strings.Join(nonBlank(purposeNames[d.Purpose], d.StaffName, d.AssignedTo), " / ")
		status := map[string]string{"operational": "Operativo", "pending": "Pendiente"}[d.Status]
		inventory = append(inventory, []string{d.Code, desc, d.RoomName, user, status})
		switch d.Kind {
		case "desktop":
			counts["pc"]++
		case "laptop":
			counts["lap"]++
		default:
			counts["other"]++
		}
	}
	if len(inventory) == 0 {
		inventory = [][]string{{"—", "Sin equipos registrados", "", "", ""}}
	}

	// VI: the tests
	var tests [][]string
	var accessCodes []string
	for _, c := range checks {
		target := strings.Join(nonBlank(c.RoomName, c.DeviceCode), " / ")
		detail := ""
		if c.Kind == "connectivity" {
			detail = strings.Join(nonBlank(c.Provider, linkNames[c.LinkKind]), ", ")
			var m []string
			if c.DownloadMbps != nil {
				m = append(m, fmt.Sprintf("bajada %.1f Mbps", *c.DownloadMbps))
			}
			if c.UploadMbps != nil {
				m = append(m, fmt.Sprintf("subida %.1f Mbps", *c.UploadMbps))
			}
			if c.LatencyMs != nil {
				m = append(m, fmt.Sprintf("latencia %d ms", *c.LatencyMs))
			}
			detail = strings.Join(nonBlank(detail, strings.Join(m, ", ")), "; ")
		} else {
			detail = "Perfil: " + c.Profile
			if c.Passed && c.DeviceCode != "" {
				accessCodes = append(accessCodes, c.DeviceCode)
			}
		}
		result := map[bool]string{true: "Conforme", false: "Con observaciones"}[c.Passed]
		tests = append(tests, []string{c.CheckedOn.Format("02/01/2006"), map[string]string{"connectivity": "Conectividad", "platform_access": "Acceso a la plataforma"}[c.Kind], target, detail,
			strings.Join(nonBlank(result, c.Result), ": ")})
	}
	if len(tests) == 0 {
		tests = [][]string{{"—", "Sin pruebas registradas", "", "", ""}}
	}
	access := "Desde los equipos evaluados se verificó el acceso a la plataforma educativa alojada en " + orBlank(f.sheet.URL) + ", utilizando perfiles autorizados (tutor, administrador). El resultado de cada prueba, su fecha y la captura asociada constan en el Anexo 04."
	if len(accessCodes) > 0 {
		access = fmt.Sprintf("Desde los equipos %s se verificó el acceso a la plataforma educativa alojada en %s, utilizando perfiles autorizados. El resultado de cada prueba, su fecha y la captura asociada constan en el Anexo 04.",
			strings.Join(uniqueStrings(accessCodes), ", "), orBlank(f.sheet.URL))
	}

	// VII: the results matrix
	var results [][]string
	for _, it := range f.items {
		results = append(results, []string{it.Element, map[bool]string{true: resultNames[domain.ItemPending], false: resultNames[it.Status]}[it.Status == domain.ItemVerified && it.Files == 0], evidenceRef(it)})
	}

	doc.Sections = []port.ReportSection{
		{Table: &port.ReportTable{Columns: []string{"DATOS GENERALES", "INFORMACIÓN"}, Widths: []float64{35, 65}, StatusColumn: -1, Rows: [][]string{
			{"Institución", inst.Name}, {"Ubicación del local", orBlank(f.report.Location)}, {"Responsable de la verificación", orBlank(verifier)},
			{"Fecha de evaluación", f.evaluatedOn()}, {"Referencia normativa", "RSG N.° 613-2014-MINEDU, numeral 6.3.2, literal c)"},
		}}},
		{Heading: "I. OBJETO", Paragraphs: []string{"El presente informe tiene por objeto describir la disponibilidad, ubicación, estado operativo, asignación funcional y conectividad de los equipos informáticos destinados a las actividades de tutoría virtual y, cuando corresponda, al soporte tecnológico de estudiantes del " + inst.Name + ", conforme a su modalidad de atención a distancia."}},
		{Heading: "II. MARCO DE REFERENCIA", Paragraphs: []string{"Se toma como referencia la Resolución de Secretaría General N.° 613-2014-MINEDU, particularmente el numeral 6.3.2, literal c), relativo a la disponibilidad de un espacio físico implementado con equipos informáticos y conectividad a internet para uso de los tutores virtuales, incluida la plataforma e-learning con las secciones previstas en su Anexo 2."}},
		{Heading: "III. IDENTIFICACIÓN DEL ESPACIO FÍSICO", Paragraphs: spaces},
		{Heading: "IV. INVENTARIO Y ASIGNACIÓN DE EQUIPOS INFORMÁTICOS", Paragraphs: []string{"La relación siguiente consigna exclusivamente los equipos constatados durante la verificación. El uso asignado se identifica como «tutoría virtual», «soporte a estudiantes», «administración» o «compartido», según corresponda a las condiciones reales."},
			Table: &port.ReportTable{Columns: []string{"CÓDIGO", "EQUIPO Y CARACTERÍSTICA PRINCIPAL", "UBICACIÓN", "DESTINO / USUARIO", "ESTADO"}, Widths: []float64{12, 36, 18, 22, 12}, Rows: inventory, StatusColumn: 4},
			After: []string{fmt.Sprintf("Cantidad total verificada: %d %s, %d %s y %d %s. La asignación y disponibilidad de cada uno queda respaldada mediante fotografías contenidas en los anexos.",
				counts["pc"], plural(counts["pc"], "computadora", "computadoras"), counts["lap"], plural(counts["lap"], "laptop", "laptops"), counts["other"], plural(counts["other"], "otro equipo de apoyo", "otros equipos de apoyo"))}},
		{Heading: "V. DESTINO DE USO Y ACTIVIDADES DE LOS TUTORES", Paragraphs: []string{
			"Los equipos asignados a tutoría virtual están destinados a actividades académicas tales como ingreso a la plataforma e-learning, organización de recursos de aprendizaje, desarrollo de sesiones virtuales, acompañamiento al estudiante, consulta y registro de participación, y evaluación de aprendizajes, conforme a las funciones efectivamente habilitadas.",
			"Los equipos identificados como compartidos no se declaran de uso exclusivo. Para cada uno se precisa el responsable y su modalidad de utilización en la relación del apartado IV.",
		}},
		{Heading: "VI. CONECTIVIDAD Y ACCESO A LA PLATAFORMA E-LEARNING",
			Table: &port.ReportTable{Columns: []string{"FECHA", "PRUEBA", "AMBIENTE / EQUIPO", "DETALLE", "RESULTADO"}, Widths: []float64{12, 16, 18, 30, 24}, Rows: tests, StatusColumn: -1},
			After: []string{access}},
		{Heading: "VII. RESULTADOS DE LA VERIFICACIÓN", Table: &port.ReportTable{Columns: []string{"ASPECTO VERIFICADO", "RESULTADO REAL", "EVIDENCIA"}, Widths: []float64{45, 22, 33}, Rows: results, StatusColumn: 1}},
		{Heading: "VIII. CONCLUSIONES", Paragraphs: []string{
			"8.1. Se ha identificado el ambiente destinado a las labores de tutoría virtual, cuya ubicación, características y soporte documental se consignan en los apartados III y IX del presente informe.",
			"8.2. El inventario del apartado IV individualiza los equipos evaluados, su estado operativo y su destino de uso, diferenciando aquellos destinados a tutores virtuales de los que sirven para apoyo a estudiantes o tienen uso compartido.",
			"8.3. Sobre la base de las verificaciones realizadas y de las evidencias adjuntas, se concluye que " + orBlank(f.report.Conclusion) + ".",
		}},
		{Heading: "IX. ANEXOS DOCUMENTALES", Paragraphs: []string{
			"Anexo 01. Registro fotográfico del ambiente y distribución de puestos de tutoría virtual.",
			"Anexo 02. Inventario detallado de computadoras, laptops y equipos asociados, identificados por código (apartado IV).",
			"Anexo 03. Registro fotográfico de equipos y, cuando corresponda, etiquetas o características técnicas.",
			"Anexo 04. Evidencias de conectividad a internet y pruebas de acceso a la plataforma e-learning.",
			"Se incorporan únicamente anexos disponibles y verificables; las capturas evitan mostrar contraseñas o datos personales de estudiantes.",
		}},
	}
	if f.report.Limitations != "" {
		doc.Sections[len(doc.Sections)-2].Paragraphs = append(doc.Sections[len(doc.Sections)-2].Paragraphs, "Limitaciones identificadas: "+f.report.Limitations+".")
	}
	add := func(annex, kind, id, label string) error {
		images, err := s.evidence(ctx, inst.ID, kind, id)
		if err != nil {
			return err
		}
		for i, img := range images {
			title := "Anexo " + annex + " · " + label
			if len(images) > 1 {
				title += fmt.Sprintf(" (%d de %d)", i+1, len(images))
			}
			doc.Annexes = append(doc.Annexes, port.ReportAnnex{Title: title, Image: img})
		}
		return nil
	}
	for _, r := range rooms {
		if r.Files > 0 {
			if err := add("01", domain.OwnerEquipmentRoom, r.ID, r.Name); err != nil {
				return PdfFile{}, err
			}
		}
	}
	for _, d := range devices {
		if d.Files > 0 {
			if err := add("03", domain.OwnerEquipmentDevice, d.ID, d.Code+" · "+equipmentKindNames[d.Kind]); err != nil {
				return PdfFile{}, err
			}
		}
	}
	for _, c := range checks {
		if c.Files > 0 {
			label := map[string]string{"connectivity": "Prueba de conectividad", "platform_access": "Acceso a la plataforma"}[c.Kind] + " · " + strings.Join(nonBlank(c.RoomName, c.DeviceCode), " / ") + " · " + c.CheckedOn.Format("02/01/2006")
			if err := add("04", domain.OwnerEquipmentCheck, c.ID, label); err != nil {
				return PdfFile{}, err
			}
		}
	}
	if err := s.itemAnnexes(ctx, inst.ID, f.items, map[string]string{"equipment": "04"}, &doc); err != nil {
		return PdfFile{}, err
	}
	content, err := s.render.TechReport(doc)
	if err != nil {
		return PdfFile{}, err
	}
	return PdfFile{Name: pdfName("equipos", f.when), Content: content}, nil
}

func uniqueStrings(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
