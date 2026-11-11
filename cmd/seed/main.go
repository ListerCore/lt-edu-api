// Command seed loads the data the platform starts with into a migrated database: the files of migrations/seed
// (each runs once; running it again only applies files added since), then the superadmin of APP__ADMIN__*.
// The Docker entrypoint runs it after migrate up, before the server starts.
package main

import (
	"context"
	"time"

	"api/internal/adapter/logging"
	"api/internal/adapter/outbound/password"
	"api/internal/adapter/outbound/postgres"
	"api/internal/config"
	"api/internal/core/service"
)

func main() {
	cfg := config.Load()
	logger := logging.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	seeder, err := postgres.NewSeeder(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize seeder")
	}
	defer seeder.Close()

	applied, err := seeder.Run(ctx)
	for _, name := range applied {
		logger.Info().Str("file", name).Msg("seeded")
	}
	if err != nil {
		logger.Fatal().Err(err).Msg("seed failed")
	}
	if len(applied) == 0 {
		logger.Info().Msg("seed files: no change")
	}

	// The superadmin needs an Argon2id hash, so it is seeded from Go rather than SQL. An existing account keeps
	// its password; the seed only guarantees the account and its role
	db, err := postgres.New(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize database pool")
	}
	defer db.Close()
	// Demo study plans come from the templates in Go (the same ones the panel offers), once
	ran, err := seeder.RunOnce(ctx, "go:0005_demo_curricula", func(ctx context.Context) error {
		n, err := service.SeedDemo(ctx, postgres.NewAcademicRepository(db), postgres.NewCurriculumRepository(db), []string{"demo-ebr", "demo-eba", "demo-ebe", "demo-cepre", "demo-institute"})
		logger.Info().Int("plans", n).Msg("demo study plans seeded")
		return err
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("demo study plans seed failed")
	}
	if ran {
		logger.Info().Str("step", "go:0005_demo_curricula").Msg("seeded")
	}
	// Demo students, guardians and enrollments, once (after the plans: institute enrollments take their units)
	ran, err = seeder.RunOnce(ctx, "go:0006_demo_people", func(ctx context.Context) error {
		n, err := service.SeedDemoPeople(ctx, postgres.NewAcademicRepository(db), postgres.NewCurriculumRepository(db), postgres.NewPeopleRepository(db), []string{"demo-ebr", "demo-eba", "demo-ebe", "demo-cepre", "demo-institute"})
		logger.Info().Int("students", n).Msg("demo students seeded")
		return err
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("demo students seed failed")
	}
	if ran {
		logger.Info().Str("step", "go:0006_demo_people").Msg("seeded")
	}
	// Demo staff, school day, teaching load and an automatic timetable, once
	ran, err = seeder.RunOnce(ctx, "go:0007_demo_teaching", func(ctx context.Context) error {
		academicRepo, curriculumRepo := postgres.NewAcademicRepository(db), postgres.NewCurriculumRepository(db)
		academic := service.NewAcademicService(academicRepo, nil, service.NewLiveService(nil, logger), logger)
		staffRepo := postgres.NewStaffRepository(db)
		teaching := service.NewTeachingService(postgres.NewTeachingRepository(db), staffRepo, curriculumRepo, academic)
		n, err := service.SeedDemoTeaching(ctx, academic, teaching, staffRepo, []string{"demo-ebr", "demo-eba", "demo-ebe", "demo-cepre", "demo-institute"})
		logger.Info().Int("classes", n).Msg("demo timetables seeded")
		return err
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("demo teaching seed failed")
	}
	if ran {
		logger.Info().Str("step", "go:0007_demo_teaching").Msg("seeded")
	}
	// Demo attendance of the last weeks (gate, justifications, staff and classes), once
	ran, err = seeder.RunOnce(ctx, "go:0008_demo_attendance", func(ctx context.Context) error {
		academic := service.NewAcademicService(postgres.NewAcademicRepository(db), nil, service.NewLiveService(nil, logger), logger)
		staffRepo, teachingRepo, attendanceRepo := postgres.NewStaffRepository(db), postgres.NewTeachingRepository(db), postgres.NewAttendanceRepository(db)
		attendance := service.NewAttendanceService(attendanceRepo, teachingRepo, staffRepo, academic)
		n, err := service.SeedDemoAttendance(ctx, attendance, attendanceRepo, []string{"demo-ebr", "demo-eba", "demo-ebe", "demo-cepre", "demo-institute"})
		logger.Info().Int("days", n).Msg("demo attendance seeded")
		return err
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("demo attendance seed failed")
	}
	if ran {
		logger.Info().Str("step", "go:0008_demo_attendance").Msg("seeded")
	}
	// Demo grades: closed terms complete, part of the running one, evaluations 0–20 in IEST and CEPRE, once
	ran, err = seeder.RunOnce(ctx, "go:0009_demo_assessment", func(ctx context.Context) error {
		academic := service.NewAcademicService(postgres.NewAcademicRepository(db), nil, service.NewLiveService(nil, logger), logger)
		staffRepo, teachingRepo := postgres.NewStaffRepository(db), postgres.NewTeachingRepository(db)
		attendance := service.NewAttendanceService(postgres.NewAttendanceRepository(db), teachingRepo, staffRepo, academic)
		assessmentRepo := postgres.NewAssessmentRepository(db)
		assessment := service.NewAssessmentService(assessmentRepo, teachingRepo, attendance, academic)
		n, err := service.SeedDemoAssessment(ctx, assessment, assessmentRepo, []string{"demo-ebr", "demo-eba", "demo-ebe", "demo-cepre", "demo-institute"})
		logger.Info().Int("marks", n).Msg("demo grades seeded")
		return err
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("demo grades seed failed")
	}
	if ran {
		logger.Info().Str("step", "go:0009_demo_assessment").Msg("seeded")
	}
	// The last grade of primaria and secundaria with the running term complete, to preview the year closing, once
	ran, err = seeder.RunOnce(ctx, "go:0010_demo_closing", func(ctx context.Context) error {
		academic := service.NewAcademicService(postgres.NewAcademicRepository(db), nil, service.NewLiveService(nil, logger), logger)
		staffRepo, teachingRepo := postgres.NewStaffRepository(db), postgres.NewTeachingRepository(db)
		attendance := service.NewAttendanceService(postgres.NewAttendanceRepository(db), teachingRepo, staffRepo, academic)
		assessmentRepo := postgres.NewAssessmentRepository(db)
		n, err := service.SeedDemoClosing(ctx, service.NewAssessmentService(assessmentRepo, teachingRepo, attendance, academic), assessmentRepo, "demo-ebr")
		logger.Info().Int("marks", n).Msg("demo closing levels seeded")
		return err
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("demo closing seed failed")
	}
	if ran {
		logger.Info().Str("step", "go:0010_demo_closing").Msg("seeded")
	}
	// The academy's question bank, mock exams with answer sheets and former students who got in, once
	ran, err = seeder.RunOnce(ctx, "go:0011_demo_exams", func(ctx context.Context) error {
		academic := service.NewAcademicService(postgres.NewAcademicRepository(db), nil, service.NewLiveService(nil, logger), logger)
		staffRepo, teachingRepo := postgres.NewStaffRepository(db), postgres.NewTeachingRepository(db)
		attendance := service.NewAttendanceService(postgres.NewAttendanceRepository(db), teachingRepo, staffRepo, academic)
		assessment := service.NewAssessmentService(postgres.NewAssessmentRepository(db), teachingRepo, attendance, academic)
		examsRepo := postgres.NewExamsRepository(db)
		n, err := service.SeedDemoExams(ctx, service.NewExamsService(examsRepo, assessment), examsRepo, "demo-cepre")
		logger.Info().Int("sheets", n).Msg("demo mock exams seeded")
		return err
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("demo exams seed failed")
	}
	if ran {
		logger.Info().Str("step", "go:0011_demo_exams").Msg("seeded")
	}
	// The institute's record: past semesters, work placements, modular certificates and graduates, once
	ran, err = seeder.RunOnce(ctx, "go:0012_demo_programs", func(ctx context.Context) error {
		academic := service.NewAcademicService(postgres.NewAcademicRepository(db), nil, service.NewLiveService(nil, logger), logger)
		staffRepo, teachingRepo := postgres.NewStaffRepository(db), postgres.NewTeachingRepository(db)
		attendance := service.NewAttendanceService(postgres.NewAttendanceRepository(db), teachingRepo, staffRepo, academic)
		assessment := service.NewAssessmentService(postgres.NewAssessmentRepository(db), teachingRepo, attendance, academic)
		programsRepo := postgres.NewProgramsRepository(db)
		n, err := service.SeedDemoPrograms(ctx, service.NewProgramsService(programsRepo, assessment), programsRepo, "demo-institute")
		logger.Info().Int("certificates", n).Msg("demo programs seeded")
		return err
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("demo programs seed failed")
	}
	if ran {
		logger.Info().Str("step", "go:0012_demo_programs").Msg("seeded")
	}
	// Fee plans, schedules, discounts, payments and reminders of the running periods, once
	ran, err = seeder.RunOnce(ctx, "go:0013_demo_finance", func(ctx context.Context) error {
		academic := service.NewAcademicService(postgres.NewAcademicRepository(db), nil, service.NewLiveService(nil, logger), logger)
		staffRepo, teachingRepo := postgres.NewStaffRepository(db), postgres.NewTeachingRepository(db)
		attendance := service.NewAttendanceService(postgres.NewAttendanceRepository(db), teachingRepo, staffRepo, academic)
		assessment := service.NewAssessmentService(postgres.NewAssessmentRepository(db), teachingRepo, attendance, academic)
		financeRepo := postgres.NewFinanceRepository(db)
		n, err := service.SeedDemoFinance(ctx, service.NewFinanceService(financeRepo, assessment), financeRepo, []string{"demo-ebr", "demo-eba", "demo-ebe", "demo-cepre", "demo-institute"})
		logger.Info().Int("payments", n).Msg("demo finance seeded")
		return err
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("demo finance seed failed")
	}
	if ran {
		logger.Info().Str("step", "go:0013_demo_finance").Msg("seeded")
	}
	// Tutoring, psychology, discipline and special-education plans and therapies, once
	ran, err = seeder.RunOnce(ctx, "go:0014_demo_wellbeing", func(ctx context.Context) error {
		academic := service.NewAcademicService(postgres.NewAcademicRepository(db), nil, service.NewLiveService(nil, logger), logger)
		staffRepo, teachingRepo := postgres.NewStaffRepository(db), postgres.NewTeachingRepository(db)
		attendance := service.NewAttendanceService(postgres.NewAttendanceRepository(db), teachingRepo, staffRepo, academic)
		wellbeingRepo := postgres.NewWellbeingRepository(db)
		n, err := service.SeedDemoWellbeing(ctx, service.NewWellbeingService(wellbeingRepo, attendance), wellbeingRepo, []string{"demo-ebr", "demo-eba", "demo-ebe", "demo-cepre", "demo-institute"})
		logger.Info().Int("records", n).Msg("demo wellbeing seeded")
		return err
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("demo wellbeing seed failed")
	}
	if ran {
		logger.Info().Str("step", "go:0014_demo_wellbeing").Msg("seeded")
	}
	// The whole running periods' attendance, and marks and paid fees for the closed years, for the reports board
	ran, err = seeder.RunOnce(ctx, "go:0015_demo_reports", func(ctx context.Context) error {
		n, err := service.SeedDemoReports(ctx, postgres.NewReportsRepository(db), postgres.NewAcademicRepository(db).InstitutionBySlug, []string{"demo-ebr", "demo-eba", "demo-ebe", "demo-cepre", "demo-institute"})
		logger.Info().Int("records", n).Msg("demo reports history seeded")
		return err
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("demo reports seed failed")
	}
	if ran {
		logger.Info().Str("step", "go:0015_demo_reports").Msg("seeded")
	}
	// Official header of the report cards: DRE, UGEL, modular code and logo
	ran, err = seeder.RunOnce(ctx, "go:0016_demo_profiles", func(ctx context.Context) error {
		n, err := service.SeedDemoProfiles(ctx, postgres.NewAcademicRepository(db))
		logger.Info().Int("institutions", n).Msg("demo profiles seeded")
		return err
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("demo profiles seed failed")
	}
	if ran {
		logger.Info().Str("step", "go:0016_demo_profiles").Msg("seeded")
	}

	auth, err := service.NewAuthService(postgres.NewAuthRepository(db), password.NewArgon2id(), nil, nil, nil, nil, service.AuthConfig{}, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize auth service")
	}
	if cfg.AdminEmail == "" {
		logger.Warn().Msg("APP__ADMIN__EMAIL is empty: no superadmin seeded")
		return
	}
	if err := auth.EnsureSuperadmin(ctx, cfg.AdminEmail, cfg.AdminPassword); err != nil {
		logger.Fatal().Err(err).Msg("superadmin seed failed")
	}
	logger.Info().Str("email", cfg.AdminEmail).Msg("superadmin ready")
}
