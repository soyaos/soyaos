package soyapack

import "testing"

func TestIndexedColumnRulesValidation(t *testing.T) {
	for _, tc := range []struct {
		rules []IndexedColumnRule
		valid bool
	}{
		{nil, true},
		{[]IndexedColumnRule{{Column: 0, Suffix: "？"}}, true},
		{[]IndexedColumnRule{{Column: -1, Prefix: "x"}}, false},
		{[]IndexedColumnRule{{Column: 1, Prefix: "x"}}, false},
		{[]IndexedColumnRule{{Column: 0}}, false},
		{[]IndexedColumnRule{{Column: 0, AllowedValues: []string{""}}}, false},
		{[]IndexedColumnRule{{Column: 0, ForbiddenSubstrings: []string{" "}}}, false},
		{[]IndexedColumnRule{{Column: 0, Prefix: "x"}, {Column: 0, Suffix: "y"}}, false},
	} {
		cfg := IndexedTable{TargetRows: 2, CandidateRows: 3, Columns: []string{"title"}, SheetName: "Topics", TimeoutSeconds: 1, ColumnRules: tc.rules}
		if err := cfg.Validate(3); (err == nil) != tc.valid {
			t.Fatalf("rules=%#v err=%v", tc.rules, err)
		}
	}
}
