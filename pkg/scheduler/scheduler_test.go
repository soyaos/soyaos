package scheduler

import (
	"context"
	"testing"
	"time"
)

func TestParseCron_AcceptsStandardForms(t *testing.T) {
	cases := []string{
		"0 9 * * *",   // daily 09:00
		"*/* * * * *", // bad — should fail
		"0 0 1 1 0",   // jan 1 midnight Sunday
		"0,15,30,45 * * * *",
	}
	want := []bool{true, false, true, true}
	for i, c := range cases {
		_, err := parseCron(c)
		if (err == nil) != want[i] {
			t.Errorf("parseCron(%q) ok=%v, want %v (err=%v)", c, err == nil, want[i], err)
		}
	}
}

func TestCronMatches_DailyNine(t *testing.T) {
	spec, err := parseCron("0 9 * * *")
	if err != nil {
		t.Fatal(err)
	}
	hit := time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC)
	miss := time.Date(2026, 5, 18, 9, 1, 0, 0, time.UTC)
	if !spec.matches(hit) {
		t.Error("9:00:00 should match '0 9 * * *'")
	}
	if spec.matches(miss) {
		t.Error("9:01:00 should NOT match '0 9 * * *'")
	}
}

func TestTimeWheel_AddRejectsEmptyJob(t *testing.T) {
	tw := NewTimeWheel()
	defer tw.Stop(context.Background())
	if err := tw.Add(Job{ID: "empty"}); err != ErrEmptyJob {
		t.Fatalf("Add(empty) = %v, want ErrEmptyJob", err)
	}
}

func TestTimeWheel_OneShotFires(t *testing.T) {
	tw := NewTimeWheel()
	defer tw.Stop(context.Background())

	fired := make(chan struct{}, 1)
	err := tw.Add(Job{
		ID:    "once",
		RunAt: time.Now().Add(500 * time.Millisecond),
		Fire:  func(context.Context) { fired <- struct{}{} },
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	select {
	case <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("one-shot job did not fire within 3s")
	}
}

func TestTimeWheel_CancelStopsJob(t *testing.T) {
	tw := NewTimeWheel()
	defer tw.Stop(context.Background())

	fired := make(chan struct{}, 1)
	_ = tw.Add(Job{
		ID:    "doomed",
		RunAt: time.Now().Add(800 * time.Millisecond),
		Fire:  func(context.Context) { fired <- struct{}{} },
	})
	if err := tw.Cancel("doomed"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	select {
	case <-fired:
		t.Fatal("canceled job fired anyway")
	case <-time.After(1500 * time.Millisecond):
	}
}
