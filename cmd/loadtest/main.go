// Command loadtest drives the virtual classroom like many students at the peak hour (phase E9 of
// docs/ceba/PLAN.md): each virtual student lists their rooms, opens one, opens a piece of material and an
// assessment, and with -write starts an attempt and hands it in. It reports requests per second and the latency
// percentiles of each call.
//
// Writes change data: run it against a copy (restore a backup into another database and start a second API on
// it), never against production.
//
//	go run ./cmd/demosessions -slug demo-eba -students 50 > s.json
//	go run ./cmd/loadtest -sessions s.json -addr localhost:5061 -duration 60s -write
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"sort"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"api/internal/adapter/inbound/grpc/gen/classroomv1"
)

type sample struct {
	call string
	took time.Duration
	err  bool
}

func main() {
	sessionsFile := flag.String("sessions", "", "JSON of cmd/demosessions with -students")
	addr := flag.String("addr", "localhost:5051", "gRPC address of the API")
	slug := flag.String("slug", "demo-eba", "institution")
	duration := flag.Duration("duration", 60*time.Second, "how long to run")
	think := flag.Duration("think", 2*time.Second, "pause of a student between actions (random up to twice this)")
	write := flag.Bool("write", false, "also start attempts and hand them in (changes data: use a copy)")
	flag.Parse()
	var sessions struct {
		Students []string `json:"students"`
	}
	raw, err := os.ReadFile(*sessionsFile)
	if err != nil {
		fail(err.Error())
	}
	if err := json.Unmarshal(raw, &sessions); err != nil || len(sessions.Students) == 0 {
		fail("the sessions file has no students (run demosessions with -students N)")
	}
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fail(err.Error())
	}
	defer conn.Close()
	client := classroomv1.NewClassroomServiceClient(conn)

	var mu sync.Mutex
	var samples []sample
	record := func(call string, start time.Time, err error) {
		mu.Lock()
		samples = append(samples, sample{call, time.Since(start), err != nil})
		mu.Unlock()
	}
	deadline := time.Now().Add(*duration)
	var wg sync.WaitGroup
	for _, token := range sessions.Students {
		wg.Add(1)
		go func(token string) {
			defer wg.Done()
			ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token)
			pause := func() { time.Sleep(time.Duration(rand.Int64N(int64(2**think) + 1))) }
			// Stagger the start, like students arriving
			time.Sleep(time.Duration(rand.Int64N(int64(*think) + 1)))
			for time.Now().Before(deadline) {
				start := time.Now()
				rooms, err := client.ListRooms(ctx, &classroomv1.ListRoomsRequest{InstitutionSlug: *slug})
				record("ListRooms", start, err)
				if err != nil || len(rooms.GetRooms()) == 0 {
					pause()
					continue
				}
				room := rooms.GetRooms()[rand.IntN(len(rooms.GetRooms()))]
				pause()
				start = time.Now()
				full, err := client.GetRoom(ctx, &classroomv1.GetRoomRequest{InstitutionSlug: *slug, Id: room.GetId()})
				record("GetRoom", start, err)
				if err != nil {
					continue
				}
				var material, tasks []string
				for _, u := range full.GetUnits() {
					for _, it := range u.GetItems() {
						switch it.GetKind() {
						case "quiz", "task":
							tasks = append(tasks, it.GetId())
						default:
							material = append(material, it.GetId())
						}
					}
				}
				if len(material) > 0 {
					pause()
					start = time.Now()
					_, err := client.RecordView(ctx, &classroomv1.RecordViewRequest{InstitutionSlug: *slug, ItemId: material[rand.IntN(len(material))]})
					record("RecordView", start, err)
				}
				if len(tasks) == 0 {
					pause()
					continue
				}
				item := tasks[rand.IntN(len(tasks))]
				pause()
				start = time.Now()
				a, err := client.GetAssessment(ctx, &classroomv1.GetAssessmentRequest{InstitutionSlug: *slug, ItemId: item})
				record("GetAssessment", start, err)
				if !*write || err != nil || !a.GetCanStart() {
					pause()
					continue
				}
				start = time.Now()
				sub, err := client.StartAttempt(ctx, &classroomv1.StartAttemptRequest{InstitutionSlug: *slug, ItemId: item})
				record("StartAttempt", start, err)
				if err != nil {
					continue
				}
				pause()
				start = time.Now()
				_, err = client.HandIn(ctx, &classroomv1.HandInRequest{InstitutionSlug: *slug, SubmissionId: sub.GetSubmission().GetId()})
				record("HandIn", start, err)
				pause()
			}
		}(token)
	}
	wg.Wait()

	byCall := map[string][]sample{}
	for _, s := range samples {
		byCall[s.call] = append(byCall[s.call], s)
	}
	calls := make([]string, 0, len(byCall))
	for c := range byCall {
		calls = append(calls, c)
	}
	sort.Strings(calls)
	fmt.Printf("%d virtual students, %s, %d requests (%.1f/s)\n\n", len(sessions.Students), *duration, len(samples), float64(len(samples))/duration.Seconds())
	fmt.Printf("%-14s %8s %7s %8s %8s %8s %8s\n", "call", "count", "errors", "p50", "p90", "p99", "max")
	for _, c := range calls {
		list := byCall[c]
		took := make([]time.Duration, len(list))
		errs := 0
		for i, s := range list {
			took[i] = s.took
			if s.err {
				errs++
			}
		}
		slices.Sort(took)
		q := func(p float64) time.Duration { return took[min(len(took)-1, int(p*float64(len(took))))].Round(time.Millisecond) }
		fmt.Printf("%-14s %8d %7d %8s %8s %8s %8s\n", c, len(list), errs, q(0.5), q(0.9), q(0.99), took[len(took)-1].Round(time.Millisecond))
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "loadtest:", msg)
	os.Exit(1)
}
