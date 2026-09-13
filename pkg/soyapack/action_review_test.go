package soyapack_test

import (
	"github.com/soyaos/soyaos/pkg/soyapack"
	"testing"
)

func TestValidateActionReviewPath(t *testing.T) {
	for _, tc := range []struct {
		path  string
		valid bool
	}{{"", true}, {"prompts/review.md", true}, {"../review.md", false}, {"/tmp/review.md", false}, {" ", false}} {
		m, err := soyapack.LoadFromFile(fixturePath(t, "agent.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		m.Actions = []soyapack.ActionDecl{{ID: "write", On: "per_row", Handler: "draft.md", ReviewHandler: tc.path}}
		if err := soyapack.Validate(m); (err == nil) != tc.valid {
			t.Fatalf("path=%q err=%v", tc.path, err)
		}
	}
}

func TestValidateActionPlanRequiresReview(t *testing.T) {
	for _, tc := range []struct {
		plan, review string
		valid        bool
	}{
		{"prompts/plan.md", "prompts/review.md", true},
		{"prompts/plan.md", "", false},
		{"../plan.md", "prompts/review.md", false},
		{"/tmp/plan.md", "prompts/review.md", false},
		{" ", "prompts/review.md", false},
	} {
		m, err := soyapack.LoadFromFile(fixturePath(t, "agent.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		m.Actions = []soyapack.ActionDecl{{ID: "write", On: "per_row", Handler: "draft.md", PlanHandler: tc.plan, ReviewHandler: tc.review}}
		if err := soyapack.Validate(m); (err == nil) != tc.valid {
			t.Fatalf("plan=%q review=%q err=%v", tc.plan, tc.review, err)
		}
	}
}
