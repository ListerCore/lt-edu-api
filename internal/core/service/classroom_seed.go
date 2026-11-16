package service

import (
	"context"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// Demo material of the CEBA's virtual classroom: a small library and, for Comunicación and Matemática of the
// advanced cycle, two units dosed for the promotional period (one open, one opening next week)
var (
	demoLibrary = []domain.LibraryItem{
		{Kind: domain.ItemLink, Title: "PerúEduca: recursos para estudiantes", Topic: "General", URL: "https://www.perueduca.pe",
			Description: "Portal del Ministerio de Educación con recursos educativos digitales."},
		{Kind: domain.ItemReading, Title: "Cómo organizar tu tiempo de estudio", Topic: "Tutoría",
			Description: "Pautas para estudiantes que trabajan y estudian a distancia.",
			Body:        "Estudiar a distancia pide constancia más que horas largas.\n\n1. Elige dos o tres momentos fijos a la semana y anótalos.\n2. Empieza por la lectura de la semana y deja la actividad para el segundo momento.\n3. Si te atrasas, escribe a tu tutor virtual antes de que se acumule.\n4. Revisa los comunicados del aula al inicio de cada semana."},
	}

	demoUnits = map[string][]struct {
		title, summary string
		items          []domain.Item
	}{
		"comunicacion": {
			{"Unidad 1 · Leemos textos de nuestra comunidad", "Leemos noticias y avisos de nuestro barrio y reconocemos su propósito.", []domain.Item{
				{Kind: domain.ItemReading, Title: "¿Para qué leemos una noticia?", Body: "Una noticia cuenta un hecho reciente que interesa a la comunidad. Responde a qué pasó, quién, cuándo, dónde y por qué.\n\nLee la noticia de la semana y subraya las respuestas a esas cinco preguntas."},
				{Kind: domain.ItemActivity, Title: "Actividad: escribe un aviso para tu comunidad", Body: "Escribe un aviso de 5 a 8 líneas sobre una actividad de tu barrio (faena, feria, reunión). Incluye qué, cuándo, dónde y a quién va dirigido. Súbelo como foto o archivo."},
				{Kind: domain.ItemLink, Title: "Material complementario: PerúEduca", Complementary: true, URL: "https://www.perueduca.pe"},
			}},
			{"Unidad 2 · Escribimos textos para nuestro trabajo", "Redactamos una solicitud y un currículum sencillo.", []domain.Item{
				{Kind: domain.ItemReading, Title: "La solicitud: partes y ejemplo", Body: "Una solicitud tiene sumilla, destinatario, datos de quien solicita, el pedido, el lugar y la fecha, y la firma."},
			}},
		},
		"matematica": {
			{"Unidad 1 · Resolvemos problemas con fracciones", "Usamos fracciones en compras, recetas y medidas del trabajo diario.", []domain.Item{
				{Kind: domain.ItemReading, Title: "Fracciones en la vida diaria", Body: "Cuando compras medio kilo de arroz o un cuarto de pollo usas fracciones. El denominador dice en cuántas partes iguales se divide y el numerador cuántas tomamos."},
				{Kind: domain.ItemActivity, Title: "Actividad: el presupuesto de la semana", Body: "Anota lo que gastas en una semana. ¿Qué fracción del total va a alimentos? ¿Y a transporte? Explica cómo lo calculaste."},
			}},
			{"Unidad 2 · Porcentajes y descuentos", "Calculamos descuentos, intereses simples y aumentos.", []domain.Item{
				{Kind: domain.ItemReading, Title: "¿Qué significa 20 % de descuento?", Body: "El 20 % es 20 de cada 100. Si algo cuesta S/ 50, el descuento es S/ 10 y pagas S/ 40."},
			}},
		},
	}
)

// SeedDemoClassroom generates the rooms of the running period of the institution, publishes them, fills the library
// and the demo units, and schedules a live session per filled room. Returns how many items it created
func SeedDemoClassroom(ctx context.Context, repo port.ClassroomRepository, slug string, now time.Time) (int, error) {
	inst, err := repo.InstitutionBySlug(ctx, slug)
	if err != nil {
		return 0, err
	}
	periodID, err := repo.RunningPeriod(ctx, inst.ID)
	if err != nil || periodID == "" {
		return 0, err
	}
	if _, err := repo.EnsureRooms(ctx, inst.ID, periodID); err != nil {
		return 0, err
	}
	rooms, err := repo.Rooms(ctx, inst.ID, domain.RoomFilter{PeriodID: periodID})
	if err != nil {
		return 0, err
	}
	library := map[string]string{}
	for _, l := range demoLibrary {
		saved, err := repo.SaveLibraryItem(ctx, inst.ID, "", "Coordinación académica", l)
		if err != nil {
			return 0, err
		}
		library[l.Topic] = saved.ID
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	nextWeek := today.AddDate(0, 0, 7)
	// Next weekday at 19:00 Lima (00:00 UTC of the following day)
	session := today.AddDate(0, 0, 2).Add(24 * time.Hour)
	hour := 60
	created := 0
	for _, room := range rooms {
		room.Status = domain.RoomPublished
		if room.Description == "" {
			room.Description = "Aula virtual de " + room.SubjectName + " para " + room.GradeName + ". Revisa cada semana el material nuevo y escribe a tu tutor virtual si tienes dudas."
		}
		if _, err := repo.UpdateRoom(ctx, inst.ID, room); err != nil {
			return created, err
		}
		units, ok := demoUnits[room.SubjectCode]
		if !ok || room.LevelName != "Ciclo Avanzado" || room.Units > 0 {
			continue
		}
		for i, u := range units {
			unit := domain.Unit{RoomID: room.ID, Title: u.title, Summary: u.summary, Published: true}
			if i == 1 {
				unit.OpensOn = &nextWeek
			}
			saved, err := repo.SaveUnit(ctx, inst.ID, unit)
			if err != nil {
				return created, err
			}
			items := append([]domain.Item{}, u.items...)
			if i == 0 {
				items = append(items,
					domain.Item{Kind: domain.ItemReading, Title: "Cómo organizar tu tiempo de estudio", LibraryItemID: library["Tutoría"], Complementary: true},
					domain.Item{Kind: domain.ItemLive, Title: "Sesión en vivo con el docente", URL: "https://meet.google.com/new", StartsAt: &session, DurationMinutes: &hour,
						Body: "Resolvemos dudas de la unidad. Si no puedes conectarte, la grabación queda aquí."})
			}
			for _, it := range items {
				it.UnitID, it.Published = saved.ID, true
				if _, err := repo.SaveItem(ctx, inst.ID, "", it); err != nil {
					return created, err
				}
				created++
			}
		}
	}
	return created, nil
}
