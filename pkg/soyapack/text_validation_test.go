package soyapack

import "testing"

func TestTextValidationBounds(t *testing.T) {
	for _, tc := range []struct {
		v  TextValidation
		ok bool
	}{
		{TextValidation{MaxChars: 130, MinChars: 60, MaxRepairs: 1}, true},
		{TextValidation{}, false},
		{TextValidation{MaxChars: 10, MinChars: 11}, false},
		{TextValidation{MaxChars: 10, MaxRepairs: 3}, false},
		{TextValidation{MaxChars: 10, ForbiddenPhrases: []string{""}}, false},
	} {
		if err := tc.v.Validate(); (err == nil) != tc.ok {
			t.Fatalf("cfg=%v err=%v", tc.v, err)
		}
	}
}

func TestIndexedPartitionValidation(t *testing.T) {
	c := IndexedTable{TargetRows: 2, CandidateRows: 4, Columns: []string{"title", "dimension"}, SheetName: "Topics", TimeoutSeconds: 1, BatchSize: 2, MaxConcurrency: 2, BatchColumn: 1, BatchValues: []string{"A", "B"}}
	if err := c.Validate(3); err != nil {
		t.Fatal(err)
	}
	c.BatchValues = []string{"A"}
	if c.Validate(3) == nil {
		t.Fatal("accepted wrong partition count")
	}
	c.BatchValues = []string{"A", "A"}
	if c.Validate(3) == nil {
		t.Fatal("accepted duplicate partitions")
	}
	c.BatchValues = []string{"A", "B"}
	c.BatchColumn = 2
	if c.Validate(3) == nil {
		t.Fatal("accepted out of range column")
	}
}
