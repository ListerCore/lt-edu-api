package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"api/internal/core/domain"
)

// apafaAt is a day relative to today at a Lima hour, for the demo meetings
func apafaAt(now time.Time, days, hour int) time.Time {
	l := now.In(lima()).AddDate(0, 0, days)
	return time.Date(l.Year(), l.Month(), l.Day(), hour, 0, 0, 0, lima())
}

// SeedDemoApafa gives the public demos (CEBA and CEBE) their APAFA: two meetings already held with their acta, the
// next ordinary meeting convened (its convocatoria goes to Comunicados), a draft not convened yet, and the board.
// Institutions that are not public or already have meetings are left alone. cmd/seed runs it once
func SeedDemoApafa(ctx context.Context, svc *ApafaService, slugs []string) (int, error) {
	total := 0
	now := svc.now()
	for _, slug := range slugs {
		inst, err := svc.repo.InstitutionBySlug(ctx, slug)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return total, err
		}
		management, err := svc.repo.Management(ctx, inst.ID)
		if err != nil {
			return total, err
		}
		existing, err := svc.repo.Meetings(ctx, inst.ID, true)
		if err != nil {
			return total, err
		}
		if management != domain.ManagementPublic || len(existing) > 0 || !apafaInstitutionKinds[inst.Kind] {
			continue
		}
		// CEBA students are adults: their meetings are for the whole community
		audience := domain.AudienceGuardians
		if inst.Kind == domain.KindEBA {
			audience = domain.AudienceCommunity
		}
		sc := apafaScope{institutionScope: institutionScope{institution: inst, role: domain.RoleSuperadmin}, userName: "Secretaría"}
		attendees := []int{86, 64}
		held := []domain.ApafaMeeting{
			{Title: "Asamblea general de inicio del año escolar", Kind: domain.ApafaAssembly, ScheduledAt: apafaAt(now, -240, 18), Place: "Patio central de la institución",
				Agenda:     "1. Informe económico del año anterior\n2. Plan de trabajo anual de la APAFA\n3. Monto de la cuota APAFA\n4. Elección del comité de vigilancia",
				Minutes:    "Con el quórum reglamentario se instaló la asamblea. El tesorero presentó el balance del año anterior, que fue aprobado por mayoría. Se presentó el plan de trabajo anual.",
				Agreements: "1. Se aprueba el balance económico.\n2. Se aprueba el plan de trabajo anual.\n3. La cuota APAFA se mantiene en S/ 30 por familia."},
			{Title: "Reunión ordinaria de medio año", Kind: domain.ApafaOrdinary, ScheduledAt: apafaAt(now, -90, 18), Modality: domain.ApafaHybrid, Place: "Sala de usos múltiples",
				MeetingURL: "https://meet.google.com/apa-fame-dio", Agenda: "1. Avance del plan de trabajo\n2. Mantenimiento de aulas\n3. Varios",
				Minutes:    "Se informó el avance del plan de trabajo y los gastos del primer semestre. Las familias propusieron priorizar el mantenimiento de los servicios higiénicos.",
				Agreements: "1. Se destina el 40 % de lo recaudado al mantenimiento de los servicios higiénicos.\n2. Faena de limpieza el último sábado del mes."},
		}
		for i, m := range held {
			m.InstitutionID, m.Audience, m.CreatedName = inst.ID, audience, sc.userName
			if m.Modality == "" {
				m.Modality = domain.ApafaInPerson
			}
			created, err := svc.repo.CreateMeeting(ctx, m)
			if err != nil {
				return total, fmt.Errorf("%s: %w", slug, err)
			}
			notified, published := m.ScheduledAt.AddDate(0, 0, -10), m.ScheduledAt.AddDate(0, 0, 2)
			created.Status, created.Minutes, created.Agreements, created.Attendees = domain.ApafaHeld, m.Minutes, m.Agreements, &attendees[i]
			created.NotifiedAt, created.MinutesAt = &notified, &published
			if _, err := svc.repo.UpdateMeeting(ctx, created); err != nil {
				return total, fmt.Errorf("%s: %w", slug, err)
			}
			total++
		}
		// The next ordinary meeting, convened: its convocatoria lands in Comunicados
		next, err := svc.repo.CreateMeeting(ctx, domain.ApafaMeeting{InstitutionID: inst.ID, Title: "Reunión ordinaria de fin de año", Kind: domain.ApafaOrdinary,
			ScheduledAt: apafaAt(now, 16, 18), Modality: domain.ApafaInPerson, Place: "Patio central de la institución", Audience: audience, CreatedName: sc.userName,
			Agenda: "1. Rendición de cuentas del año\n2. Actividad de clausura\n3. Propuesta de cuota APAFA para el próximo año\n4. Varios"})
		if err != nil {
			return total, fmt.Errorf("%s: %w", slug, err)
		}
		if _, err := svc.publish(ctx, sc, next, noticeConvene); err != nil {
			return total, fmt.Errorf("%s: %w", slug, err)
		}
		// A board meeting still being prepared (families do not see it)
		if _, err := svc.repo.CreateMeeting(ctx, domain.ApafaMeeting{InstitutionID: inst.ID, Title: "Consejo directivo: balance y presupuesto", Kind: domain.ApafaBoard,
			ScheduledAt: apafaAt(now, 30, 17), Modality: domain.ApafaVirtual, MeetingURL: "https://meet.google.com/con-sejo-dir", Audience: audience, CreatedName: sc.userName,
			Agenda: "1. Balance del año\n2. Presupuesto del próximo año"}); err != nil {
			return total, fmt.Errorf("%s: %w", slug, err)
		}
		total += 2
		year := now.In(lima()).Year()
		from, to := time.Date(year, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(year+1, 2, 28, 0, 0, 0, 0, time.UTC)
		for _, b := range []domain.ApafaBoardMember{
			{FullName: "María Elena Quispe Huamán", Position: "president", Phone: "987654321"},
			{FullName: "Jorge Luis Condori Mamani", Position: "vice_president", Phone: "986543210"},
			{FullName: "Rosa Amelia Torres Ccori", Position: "secretary"},
			{FullName: "Pedro Pablo Huanca Rojas", Position: "treasurer", Phone: "985432109"},
			{FullName: "Lucía Flores Apaza", Position: "spokesperson"},
		} {
			b.InstitutionID, b.StartsOn, b.EndsOn = inst.ID, from, to
			if _, err := svc.repo.SaveBoardMember(ctx, b); err != nil {
				return total, fmt.Errorf("%s: %w", slug, err)
			}
		}
	}
	return total, nil
}
