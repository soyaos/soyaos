package soyapack

import "testing"

func TestIndexedTableConfigValidation(t *testing.T) {
	valid := func() IndexedTable {
		return IndexedTable{TargetRows: 500, CandidateRows: 540, Columns: []string{"标题", "钩子"}, SheetName: "Topics", MaxRepairs: 1, TimeoutSeconds: 300}
	}
	c := valid()
	if err := c.Validate(3); err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(2); err == nil {
		t.Fatal("accepted two steps")
	}
	for _, mutate := range []func(*IndexedTable){func(c *IndexedTable) { c.TargetRows = 0 }, func(c *IndexedTable) { c.CandidateRows = 499 }, func(c *IndexedTable) { c.CandidateRows = 10001 }, func(c *IndexedTable) { c.Columns = nil }, func(c *IndexedTable) { c.Columns = []string{"A", "A"} }, func(c *IndexedTable) { c.Columns = []string{" "} }, func(c *IndexedTable) { c.SheetName = "bad/name" }, func(c *IndexedTable) { c.MaxRepairs = -1 }, func(c *IndexedTable) { c.MaxRepairs = 4 }, func(c *IndexedTable) { c.TimeoutSeconds = 0 }, func(c *IndexedTable) { c.TimeoutSeconds = 3601 }} {
		c := valid()
		mutate(&c)
		if err := c.Validate(3); err == nil {
			t.Fatalf("accepted %#v", c)
		}
	}
}

func TestIndexedTableBatchConfig(t *testing.T) {
	for _, tc := range []struct {
		size, concurrency int
		valid             bool
	}{{0, 0, true}, {90, 6, true}, {540, 1, true}, {0, 1, false}, {1, 0, false}, {-1, 2, false}, {541, 2, false}, {90, 9, false}, {1, 2, false}} {
		cfg := IndexedTable{TargetRows: 500, CandidateRows: 540, Columns: []string{"title"}, SheetName: "Topics", TimeoutSeconds: 300, BatchSize: tc.size, MaxConcurrency: tc.concurrency}
		err := cfg.Validate(3)
		if (err == nil) != tc.valid {
			t.Fatalf("size=%d concurrency=%d err=%v", tc.size, tc.concurrency, err)
		}
	}
}
