package service

import (
	"context"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// SeedDemoClassroomAssessments adds, to the first unit of each Matemática and Comunicación room of the advanced
// cycle that has material, a quiz open for two weeks and a task, both assessing the first competency of the area.
// Returns how many it created
func SeedDemoClassroomAssessments(ctx context.Context, repo port.ClassroomRepository, slug string, now time.Time) (int, error) {
	inst, err := repo.InstitutionBySlug(ctx, slug)
	if err != nil {
		return 0, err
	}
	periodID, err := repo.RunningPeriod(ctx, inst.ID)
	if err != nil || periodID == "" {
		return 0, err
	}
	rooms, err := repo.Rooms(ctx, inst.ID, domain.RoomFilter{PeriodID: periodID})
	if err != nil {
		return 0, err
	}
	opens := now.Add(-24 * time.Hour)
	closes := now.Add(14 * 24 * time.Hour)
	created := 0
	for _, room := range rooms {
		if room.LevelName != "Ciclo Avanzado" || (room.SubjectCode != "matematica" && room.SubjectCode != "comunicacion") || room.Units == 0 {
			continue
		}
		units, err := repo.Units(ctx, room.ID, false, "")
		if err != nil {
			return created, err
		}
		comps, err := repo.RoomCompetencies(ctx, inst.ID, room.ID)
		if err != nil || len(units) == 0 || len(comps) == 0 {
			return created, err
		}
		quizTitle, questions := "Cuestionario: fracciones en la vida diaria", []domain.QuizQuestion{
			{Kind: domain.QuestionSingle, Prompt: "Compras medio kilo de arroz a S/ 4.00 el kilo. ¿Cuánto pagas?", Options: []string{"S/ 2.00", "S/ 4.00", "S/ 8.00"}, Correct: []int{0}, Points: 2},
			{Kind: domain.QuestionMultiple, Prompt: "¿Cuáles de estas fracciones son iguales a 1/2?", Options: []string{"2/4", "3/6", "2/3", "5/10"}, Correct: []int{0, 1, 3}, Points: 2},
			{Kind: domain.QuestionShort, Prompt: "¿Cuántos cuartos hay en un entero? Escribe el número.", Accepted: []string{"4", "cuatro"}, Points: 1},
			{Kind: domain.QuestionOpen, Prompt: "Cuenta una situación de tu trabajo o tu casa en la que uses fracciones.", Points: 5},
		}
		if room.SubjectCode == "comunicacion" {
			quizTitle, questions = "Cuestionario: la noticia", []domain.QuizQuestion{
				{Kind: domain.QuestionSingle, Prompt: "¿Para qué sirve una noticia?", Options: []string{"Para informar un hecho reciente", "Para vender un producto", "Para contar un cuento"}, Correct: []int{0}, Points: 2},
				{Kind: domain.QuestionMultiple, Prompt: "¿Qué preguntas responde una noticia?", Options: []string{"Qué pasó", "Quién", "Cuánto cuesta", "Dónde"}, Correct: []int{0, 1, 3}, Points: 2},
				{Kind: domain.QuestionOpen, Prompt: "Resume en tres líneas una noticia de tu barrio.", Points: 5},
			}
		}
		quiz, err := repo.SaveItem(ctx, inst.ID, "", domain.Item{UnitID: units[0].ID, Kind: domain.ItemQuiz, Title: quizTitle, Published: true,
			Body: "Responde con calma. Tienes dos intentos; se considera el último."})
		if err != nil {
			return created, err
		}
		if err := repo.EnsureAssessment(ctx, quiz.ID); err != nil {
			return created, err
		}
		if _, err := repo.SaveAssessment(ctx, inst.ID, domain.Assessment{ItemID: quiz.ID, OpensAt: &opens, ClosesAt: &closes, MaxAttempts: 2,
			CompetencyIDs: []string{comps[0].ID}, ShowAnswers: true}, questions); err != nil {
			return created, err
		}
		task, err := repo.SaveItem(ctx, inst.ID, "", domain.Item{UnitID: units[0].ID, Kind: domain.ItemTask, Title: "Tarea: aplica lo aprendido", Published: true,
			Body: "Resuelve la actividad de la unidad y sube una foto o un archivo con tu trabajo. Si no tienes internet, entrégalo a tu tutor."})
		if err != nil {
			return created, err
		}
		if err := repo.EnsureAssessment(ctx, task.ID); err != nil {
			return created, err
		}
		if _, err := repo.SaveAssessment(ctx, inst.ID, domain.Assessment{ItemID: task.ID, OpensAt: &opens, ClosesAt: &closes, MaxAttempts: 1,
			CompetencyIDs: []string{comps[0].ID}, ShowAnswers: true}, nil); err != nil {
			return created, err
		}
		created += 2
	}
	return created, nil
}
