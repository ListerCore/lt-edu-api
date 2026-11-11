package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"api/internal/core/domain"
)

// FinanceSeeder is what the demo needs besides the repository (cmd/seed only)
type FinanceSeeder interface {
	HasFinance(ctx context.Context, institutionID string) (bool, error)
}

func lastDay(year int, month time.Month) time.Time {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC)
}

// monthly builds an enrollment fee and monthly tuition from a month to another (inclusive), due on the last day
func monthly(enrollment domain.Money, enrollOn time.Time, tuition domain.Money, year int, from, to time.Month) []domain.FeeItem {
	var items []domain.FeeItem
	if enrollment > 0 {
		items = append(items, domain.FeeItem{Concept: "enrollment", Name: "Matrícula", Amount: enrollment, DueOn: enrollOn})
	}
	months := []string{"", "enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}
	for m := from; m <= to; m++ {
		items = append(items, domain.FeeItem{Concept: "tuition", Name: "Pensión de " + months[m], Amount: tuition, DueOn: lastDay(year, m)})
	}
	return items
}

// SeedDemoFinance gives the demo institutions their fee plans for the running periods, the students' schedules with
// a few scholarships and sibling discounts, the payments made so far (on time, late or not yet) and reminders to
// the families who owe. cmd/seed runs it once
func SeedDemoFinance(ctx context.Context, svc *FinanceService, seeder FinanceSeeder, slugs []string) (int, error) {
	total := 0
	for n, slug := range slugs {
		inst, err := svc.assessment.academic.repo.InstitutionBySlug(ctx, slug)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return total, err
		}
		if done, err := seeder.HasFinance(ctx, inst.ID); err != nil || done {
			if err != nil {
				return total, err
			}
			continue
		}
		count, err := seedFinance(ctx, svc, inst, rand.New(rand.NewPCG(uint64(n+40), 10)))
		total += count
		if err != nil {
			return total, fmt.Errorf("%s: %w", slug, err)
		}
	}
	return total, nil
}

func seedFinance(ctx context.Context, svc *FinanceService, inst domain.Institution, rnd *rand.Rand) (int, error) {
	repo := svc.repo
	periods, err := svc.assessment.academic.repo.Periods(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	levels, err := svc.assessment.academic.repo.Levels(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	levelID := func(code string) string {
		for _, l := range levels {
			if l.Code == code {
				return l.ID
			}
		}
		return ""
	}
	today := svc.today()
	var plans []domain.FeePlan
	for _, p := range periods {
		if p.Status != domain.PeriodActive {
			continue
		}
		y := p.StartsOn.Year()
		switch inst.Kind {
		case domain.KindEBR:
			for _, x := range []struct {
				code    string
				tuition domain.Money
			}{{"inicial", 38000}, {"primaria", 42000}, {"secundaria", 46000}} {
				plans = append(plans, domain.FeePlan{PeriodID: p.ID, LevelID: levelID(x.code), Name: "Pensiones " + p.Name, Items: monthly(35000, p.StartsOn.AddDate(0, 0, -7), x.tuition, y, time.March, time.December)})
			}
		case domain.KindEBA:
			plans = append(plans, domain.FeePlan{PeriodID: p.ID, Name: "Pensiones " + p.Name, Items: monthly(10000, p.StartsOn, 15000, y, time.April, time.December)})
		case domain.KindEBE:
			plans = append(plans, domain.FeePlan{PeriodID: p.ID, Name: "Pensiones " + p.Name, Items: monthly(20000, p.StartsOn.AddDate(0, 0, -7), 30000, y, time.March, time.December)})
		case domain.KindInstitute:
			plans = append(plans, domain.FeePlan{PeriodID: p.ID, Name: "Pensiones " + p.Name, Items: monthly(25000, p.StartsOn, 38000, y, time.August, time.December)})
		case domain.KindCEPRE:
			half := p.StartsOn.AddDate(0, 0, int(p.EndsOn.Sub(p.StartsOn).Hours()/48))
			plans = append(plans, domain.FeePlan{PeriodID: p.ID, Name: "Cuotas " + p.Name, Items: []domain.FeeItem{
				{Concept: "tuition", Name: "Primera cuota", Amount: 32000, DueOn: p.StartsOn},
				{Concept: "tuition", Name: "Segunda cuota", Amount: 32000, DueOn: half},
			}})
		}
	}
	created := 0
	for _, pl := range plans {
		if pl.LevelID == "" && inst.Kind == domain.KindEBR {
			continue
		}
		saved, err := repo.SavePlan(ctx, inst.ID, pl)
		if err != nil {
			return created, err
		}
		enrolled, err := svc.periodStudents(ctx, inst.ID, pl.PeriodID, pl.LevelID)
		if err != nil {
			return created, err
		}
		for _, id := range enrolled {
			switch h := hashOf("discount:"+id) % 100; {
			case h < 3:
				if err := repo.SaveDiscount(ctx, inst.ID, domain.Discount{StudentID: id, PeriodID: pl.PeriodID, Percent: 50, TuitionOnly: true, Reason: "Beca por rendimiento académico"}); err != nil {
					return created, err
				}
			case h < 8:
				if err := repo.SaveDiscount(ctx, inst.ID, domain.Discount{StudentID: id, PeriodID: pl.PeriodID, Percent: 15, TuitionOnly: true, Reason: "Descuento por hermanos"}); err != nil {
					return created, err
				}
			}
		}
		if _, err := repo.GenerateCharges(ctx, inst.ID, saved.ID); err != nil {
			return created, err
		}
		methods := []string{"cash", "cash", "cash", "yape", "yape", "yape", "transfer", "transfer", "plin", "deposit"}
		var reminders []domain.Reminder
		for _, id := range enrolled {
			charges, err := repo.Charges(ctx, inst.ID, id)
			if err != nil {
				return created, err
			}
			habit := hashOf("pays:"+id) % 100
			skips := 0
			if habit >= 92 {
				skips = 1 + int(habit%3)
			}
			var due []domain.Charge
			for _, c := range charges {
				if c.PeriodID == pl.PeriodID && c.DueOn.Before(today) && c.Balance() > 0 {
					due = append(due, c)
				}
			}
			name, _, err := repo.StudentName(ctx, inst.ID, id)
			if err != nil {
				return created, err
			}
			payer := strings.Fields(name)[0]
			for i, c := range due {
				if i >= len(due)-skips {
					break
				}
				paid := c.DueOn.AddDate(0, 0, -rnd.IntN(12))
				if habit >= 80 && habit < 92 {
					paid = c.DueOn.AddDate(0, 0, 5+rnd.IntN(30))
					if paid.After(today) {
						continue
					}
				}
				if paid.After(today) {
					paid = today
				}
				m := methods[rnd.IntN(len(methods))]
				ref := ""
				if m != "cash" {
					ref = fmt.Sprintf("%08d", rnd.IntN(100000000))
				}
				if _, err := repo.CreatePayment(ctx, inst.ID, domain.Payment{StudentID: id, PaidOn: paid, Amount: c.Balance(), Method: m, Reference: ref,
					Payer: "Familia de " + payer, ReceivedName: "Tesorería", Allocations: []domain.Allocation{{ChargeID: c.ID, Amount: c.Balance()}}}, ""); err != nil {
					return created, err
				}
				created++
			}
			if skips > 0 && len(due) > 0 {
				var owed domain.Money
				for _, c := range due[max(0, len(due)-skips):] {
					owed += c.Balance()
				}
				reminders = append(reminders, domain.Reminder{StudentID: id, Overdue: owed, SentName: "Tesorería",
					Message: fmt.Sprintf("Estimada familia: el estado de cuenta de %s tiene cuotas vencidas por %s. Puede acercarse a tesorería o pagar por transferencia, Yape o Plin. Si ya pagó, no considere este mensaje.", name, FormatSoles(owed))})
			}
		}
		if len(reminders) > 0 {
			if err := repo.AddReminders(ctx, inst.ID, reminders); err != nil {
				return created, err
			}
		}
	}
	return created, nil
}

// periodStudents are the students enrolled (active) in a period, of one level or all
func (s *FinanceService) periodStudents(ctx context.Context, institutionID, periodID, levelID string) ([]string, error) {
	sections, err := s.assessment.academic.repo.Sections(ctx, institutionID, periodID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, sec := range sections {
		if levelID != "" && sec.LevelID != levelID {
			continue
		}
		students, err := s.assessment.repo.Students(ctx, institutionID, sec.ID, "")
		if err != nil {
			return nil, err
		}
		for _, st := range students {
			if !slices.Contains(out, st.ID) {
				out = append(out, st.ID)
			}
		}
	}
	return out, nil
}
