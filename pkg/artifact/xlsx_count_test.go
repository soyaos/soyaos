package artifact

import "testing"

func TestXLSXPrimaryRowCount(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rows    [][]any
		wantErr bool
	}{
		{"exact", [][]any{{"a"}, {"b"}}, false},
		{"short", [][]any{{"a"}}, true},
		{"blank_padding", [][]any{{"a"}, {"  "}}, true},
		{"nil_padding", [][]any{{"a"}, {nil}}, true},
		{"outside_columns", [][]any{{"a"}, {nil, "b"}}, true},
		{"excess", [][]any{{"a"}, {"b"}, {"c"}}, true},
		{"numeric_boolean", [][]any{{0}, {false}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := XLSXSnapshot{Sheets: []XLSXSheet{{Columns: []XLSXColumn{{Header: "title"}}, Rows: tc.rows}, {Rows: [][]any{{"supplement"}}}}}
			if err := s.ValidatePrimaryRowCount(2); (err != nil) != tc.wantErr {
				t.Fatalf("count: %v", err)
			}
		})
	}
	if err := (XLSXSnapshot{}).ValidatePrimaryRowCount(2); err == nil {
		t.Fatal("missing sheet accepted")
	}
	if err := (XLSXSnapshot{}).ValidatePrimaryRowCount(0); err == nil {
		t.Fatal("nonpositive count accepted")
	}
}

func TestXLSXNormalizePublicContract(t *testing.T) {
	s := XLSXSnapshot{Sheets: []XLSXSheet{{Columns: []XLSXColumn{{Header: "title"}}, Rows: [][]any{{"a"}}}}}
	for _, input := range []any{s, &s} {
		got, err := NormalizeXLSXSnapshot(input)
		if err != nil {
			t.Fatal(err)
		}
		if err := got.ValidatePrimaryRowCount(1); err != nil {
			t.Fatal(err)
		}
	}
	var nilSnapshot *XLSXSnapshot
	for _, input := range []any{nil, nilSnapshot, "unsupported"} {
		if _, err := NormalizeXLSXSnapshot(input); err == nil {
			t.Fatal("invalid snapshot accepted")
		}
	}
}
