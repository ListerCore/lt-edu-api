// Command demosessions opens one panel session per role of a demo institution, for the role walkthrough and the
// load test (phase E9 of docs/ceba/PLAN.md). Each role signs in as a staff member or student of the demo (their
// email), so the data each one sees is real; accounts that do not exist yet are created and marked to be removed.
//
//	go run ./cmd/demosessions -slug demo-eba > sessions.json      # {"director": "<token>", …}
//	go run ./cmd/demosessions -slug demo-eba -students 50 > s.json # also 50 student sessions (load test)
//	go run ./cmd/demosessions -cleanup                             # removes those sessions and accounts
//
// It refuses to run in production and on institutions whose slug does not start with "demo-".
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"api/internal/config"
)

const (
	agent  = "demo-walkthrough"
	marker = " · recorrido de prueba"
)

// roles maps each role to who plays it: a staff member by position, a section tutor, a teacher, a student
var roles = []struct{ code, key, pick string }{
	{"director", "director", `SELECT email FROM people.staff WHERE institution_id = $1 AND position ILIKE 'Director%' AND email IS NOT NULL ORDER BY code LIMIT 1`},
	{"academic_coordinator", "coordinator", `SELECT email FROM people.staff WHERE institution_id = $1 AND position ILIKE 'Coordinador%' AND email IS NOT NULL ORDER BY code LIMIT 1`},
	{"secretary", "secretary", `SELECT email FROM people.staff WHERE institution_id = $1 AND position ILIKE 'Secretar%' AND email IS NOT NULL ORDER BY code LIMIT 1`},
	{"psychologist", "psychologist", `SELECT email FROM people.staff WHERE institution_id = $1 AND position ILIKE 'Psic%' AND email IS NOT NULL ORDER BY code LIMIT 1`},
	{"treasurer", "treasurer", `SELECT email FROM people.staff WHERE institution_id = $1 AND position ILIKE 'Tesor%' AND email IS NOT NULL ORDER BY code LIMIT 1`},
	{"tutor", "tutor", `SELECT st.email FROM people.staff st WHERE st.institution_id = $1 AND st.email IS NOT NULL
		AND EXISTS (SELECT 1 FROM academic.sections sec JOIN academic.periods p ON p.id = sec.period_id AND p.status = 'active' WHERE sec.tutor_id = st.id)
		ORDER BY (st.position ILIKE 'Tutor%') DESC, st.code LIMIT 1`},
	{"teacher", "teacher", `SELECT st.email FROM people.staff st WHERE st.institution_id = $1 AND st.is_teacher AND st.email IS NOT NULL
		AND EXISTS (SELECT 1 FROM academic.assignments a WHERE a.staff_id = st.id) ORDER BY st.code LIMIT 1`},
	{"technical_lead", "technicalLead", `SELECT 'tecnico.recorrido@' || $1::text || '.demo.test'`},
	{"student", "student", `SELECT s.email FROM people.students s JOIN enrollment.enrollments e ON e.student_id = s.id AND e.status = 'active'
		JOIN academic.sections sec ON sec.id = e.section_id JOIN academic.periods p ON p.id = e.period_id AND p.status = 'active'
		WHERE s.institution_id = $1 AND s.email IS NOT NULL ORDER BY (sec.attention_mode = 'distance') DESC, s.code LIMIT 1`},
}

func token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func main() {
	slug := flag.String("slug", "demo-eba", "demo institution")
	students := flag.Int("students", 0, "also open this many student sessions (load test)")
	cleanup := flag.Bool("cleanup", false, "remove the sessions and the accounts this command created")
	flag.Parse()
	cfg := config.Load()
	if cfg.AppEnv == "production" {
		fail("demosessions does not run in production")
	}
	if !strings.HasPrefix(*slug, "demo-") {
		fail("only demo institutions (demo-…)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fail(err.Error())
	}
	defer db.Close()
	if *cleanup {
		tag, err := db.Exec(ctx, `DELETE FROM iam.sessions WHERE user_agent = $1`, agent)
		if err != nil {
			fail(err.Error())
		}
		users, err := db.Exec(ctx, `DELETE FROM iam.users WHERE full_name LIKE '%' || $1`, marker)
		if err != nil {
			fail(err.Error())
		}
		fmt.Fprintf(os.Stderr, "removed %d sessions and %d accounts\n", tag.RowsAffected(), users.RowsAffected())
		return
	}
	var inst string
	if err := db.QueryRow(ctx, `SELECT id::text FROM iam.institutions WHERE slug = $1`, *slug).Scan(&inst); err != nil {
		fail("institution " + *slug + ": " + err.Error())
	}
	out := map[string]any{}
	open := func(roleCode, email string) (string, error) {
		var userID string
		// The account of that email, or a new one marked for cleanup
		err := db.QueryRow(ctx, `WITH found AS (SELECT id FROM iam.users WHERE lower(email) = lower($1)),
			made AS (INSERT INTO iam.users (email, full_name, status, email_verified_at)
				SELECT $1, split_part($1, '@', 1) || $2, 'active', now() WHERE NOT EXISTS (SELECT 1 FROM found) RETURNING id)
			SELECT id::text FROM found UNION ALL SELECT id::text FROM made`, email, marker).Scan(&userID)
		if err != nil {
			return "", err
		}
		var roleID string
		err = db.QueryRow(ctx, `WITH r AS (SELECT id FROM iam.roles WHERE code = $2),
			found AS (SELECT ur.id FROM iam.user_roles ur, r WHERE ur.user_id = $1 AND ur.role_id = r.id AND ur.institution_id = $3),
			made AS (INSERT INTO iam.user_roles (user_id, role_id, role_scope, institution_id) SELECT $1, r.id, 'institution', $3 FROM r WHERE NOT EXISTS (SELECT 1 FROM found) RETURNING id)
			SELECT id::text FROM found UNION ALL SELECT id::text FROM made`, userID, roleCode, inst).Scan(&roleID)
		if err != nil {
			return "", err
		}
		t := token()
		_, err = db.Exec(ctx, `INSERT INTO iam.sessions (user_id, token_hash, mfa_method, user_agent, expires_at, active_user_role_id)
			VALUES ($1, sha256(convert_to($2, 'UTF8')), 'email', $3, now() + interval '8 hours', $4)`, userID, t, agent, roleID)
		return t, err
	}
	for _, r := range roles {
		var email string
		if err := db.QueryRow(ctx, r.pick, inst).Scan(&email); err != nil {
			fmt.Fprintf(os.Stderr, "%s: nobody plays it (%v)\n", r.key, err)
			continue
		}
		if r.code == "technical_lead" {
			email = "tecnico.recorrido@" + *slug + ".demo.test"
		}
		t, err := open(r.code, email)
		if err != nil {
			fail(r.key + ": " + err.Error())
		}
		out[r.key] = t
	}
	if *students > 0 {
		rows, err := db.Query(ctx, `SELECT s.email FROM people.students s JOIN enrollment.enrollments e ON e.student_id = s.id AND e.status = 'active'
			JOIN academic.periods p ON p.id = e.period_id AND p.status = 'active' WHERE s.institution_id = $1 AND s.email IS NOT NULL ORDER BY s.code LIMIT $2`, inst, *students)
		if err != nil {
			fail(err.Error())
		}
		var emails []string
		for rows.Next() {
			var e string
			if err := rows.Scan(&e); err == nil {
				emails = append(emails, e)
			}
		}
		rows.Close()
		var list []string
		for _, e := range emails {
			t, err := open("student", e)
			if err != nil {
				fail(err.Error())
			}
			list = append(list, t)
		}
		out["students"] = list
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "demosessions:", msg)
	os.Exit(1)
}
