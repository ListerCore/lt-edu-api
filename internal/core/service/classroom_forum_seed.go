package service

import (
	"context"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// SeedDemoClassroomForums adds to the first unit of each advanced-cycle room with material a debate forum with the
// teacher's pinned welcome. Returns how many forums it created
func SeedDemoClassroomForums(ctx context.Context, repo port.ClassroomRepository, slug string) (int, error) {
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
	created := 0
	for _, room := range rooms {
		if room.LevelName != "Ciclo Avanzado" || room.Units == 0 {
			continue
		}
		units, err := repo.Units(ctx, room.ID, false, "")
		if err != nil || len(units) == 0 {
			return created, err
		}
		item, err := repo.SaveItem(ctx, inst.ID, "", domain.Item{UnitID: units[0].ID, Kind: domain.ItemForum, Title: "Foro de la unidad: ¿cómo lo usas en tu vida diaria?", Published: true,
			Body: "Cuenta un ejemplo de tu trabajo o tu casa relacionado con la unidad y responde al menos a un compañero."})
		if err != nil {
			return created, err
		}
		if err := repo.EnsureForum(ctx, item.ID); err != nil {
			return created, err
		}
		name := room.StaffName
		if name == "" {
			name = "Docente del aula"
		}
		welcome, err := repo.CreatePost(ctx, domain.ForumPost{ItemID: item.ID, AuthorName: name, AuthorRole: "teacher",
			Body: "¡Bienvenidos al foro! Aquí compartimos ejemplos y dudas de la unidad. Escriban con respeto y respondan a sus compañeros."})
		if err != nil {
			return created, err
		}
		welcome.Pinned = true
		if err := repo.ModeratePost(ctx, welcome); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}
