// xlsx_renderer_test.go round-trips the rendered workbook through excelize
// itself to assert that headers/values/widths/filters/hyperlinks survive
// the encode→decode cycle. This is the cross-form proof for the
// "Excel as a first-class output" Aha on DD-010 / DD-012 (APP-500).
package artifact

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestXLSXRenderer_TypedSnapshot_RoundTrip(t *testing.T) {
	r := XLSXRenderer{Schema: "estatemuse.v1"}
	snap := XLSXSnapshot{
		Sheets: []XLSXSheet{
			{
				Name: "Topics",
				Columns: []XLSXColumn{
					{Header: "Topic", Width: 40},
					{Header: "Score", Width: 12, Conditional: "3color"},
					{Header: "Tier", Width: 8, Validation: []string{"A", "B", "C"}},
				},
				Rows: [][]any{
					{"Bloomberg AI rules", 9, "A"},
					{"Verge product spec", 7, "B"},
					{"Random blog", 4, "C"},
					{"Late breaking story", 8, "A"},
					{"Stale update", 3, "C"},
				},
				PerRowActionURL: "https://example.com/agents/news-beam/actions/star/{row_id}",
				FreezeHeader:    true,
			},
		},
	}

	var buf bytes.Buffer
	art, err := r.Render(context.Background(), snap, &buf)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if art.Kind != KindXLSX {
		t.Fatalf("Kind = %q, want %q", art.Kind, KindXLSX)
	}
	if art.MIMEType != XLSXMIME {
		t.Fatalf("MIMEType = %q, want %q", art.MIMEType, XLSXMIME)
	}
	if art.Schema != "estatemuse.v1" {
		t.Fatalf("Schema = %q", art.Schema)
	}
	if art.Size <= 0 || art.Size != int64(buf.Len()) {
		t.Fatalf("Size = %d, buf.Len = %d", art.Size, buf.Len())
	}
	if art.Metadata["extension"] != ".xlsx" {
		t.Fatalf("extension metadata missing: %+v", art.Metadata)
	}

	// Now decode the produced bytes through excelize itself and verify
	// the content survived the round-trip.
	f, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer f.Close()

	if got := f.GetSheetList(); len(got) != 1 || got[0] != "Topics" {
		t.Fatalf("GetSheetList = %v, want [Topics]", got)
	}

	header, err := f.GetCellValue("Topics", "A1")
	if err != nil {
		t.Fatalf("GetCellValue A1: %v", err)
	}
	if header != "Topic" {
		t.Fatalf("A1 = %q, want Topic", header)
	}
	v, _ := f.GetCellValue("Topics", "C2")
	if v != "A" {
		t.Fatalf("C2 = %q, want A", v)
	}

	// Column width.
	w, err := f.GetColWidth("Topics", "A")
	if err != nil {
		t.Fatalf("GetColWidth A: %v", err)
	}
	if w < 39 || w > 41 {
		t.Fatalf("col A width = %f, want ~40", w)
	}

	// Hyperlink on A2 must exist with the {row_id} substituted.
	hasLink, link, err := f.GetCellHyperLink("Topics", "A2")
	if err != nil {
		t.Fatalf("GetCellHyperLink A2: %v", err)
	}
	if !hasLink {
		t.Fatalf("A2 has no hyperlink")
	}
	if !strings.Contains(link, "/star/1") {
		t.Fatalf("A2 link %q lacks /star/1 substitution", link)
	}
}

func TestXLSXRenderer_MapSnapshot_AcceptsJSONShape(t *testing.T) {
	// Simulates the shape a caller decoding JSON into map[string]any would
	// hand to Render (e.g. an action handler reading a snapshot from the
	// model's tool output).
	snapshot := map[string]any{
		"sheets": []any{
			map[string]any{
				"name": "S",
				"columns": []any{
					map[string]any{"header": "K", "width": float64(20)},
					map[string]any{"header": "V"},
				},
				"rows": []any{
					[]any{"a", 1.0},
					[]any{"b", 2.0},
				},
			},
		},
	}
	var buf bytes.Buffer
	if _, err := (XLSXRenderer{Schema: "t.v1"}).Render(context.Background(), snapshot, &buf); err != nil {
		t.Fatalf("Render(map): %v", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer f.Close()
	if got, _ := f.GetCellValue("S", "B3"); got != "2" {
		t.Fatalf("B3 = %q, want 2", got)
	}
}

func TestXLSXRenderer_RejectsEmptySnapshot(t *testing.T) {
	_, err := (XLSXRenderer{}).Render(context.Background(), XLSXSnapshot{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for empty snapshot")
	}
}

func TestXLSXRenderer_RegistersAsRenderer(t *testing.T) {
	reg := NewRegistry()
	reg.Register(XLSXRenderer{Schema: "t.v1"})
	if _, ok := reg.Lookup(KindXLSX); !ok {
		t.Fatal("XLSXRenderer did not register under KindXLSX")
	}
}
