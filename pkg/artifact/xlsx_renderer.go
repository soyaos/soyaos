// xlsx_renderer.go renders the "xlsx" Artifact form (EstateMuse Aha #1:
// Excel as a first-class output — DD-010 / DD-012). EstateMuse and any
// per-row Action agents emit one or more sheets where each row is a
// candidate the user can click through to a deeper investigation. The
// generated workbook therefore needs:
//
//   - per-sheet column widths and an AutoFilter on the header row,
//   - optional conditional formatting per column (heatmap-style),
//   - optional dropdown validation per column (closed enum lists),
//   - per-row hyperlinks so a single click jumps back to the agent's
//     per-row action endpoint (APP-502 / APP-503).
//
// The renderer uses xuri/excelize/v2 which is pure Go (no CGO) and is
// installed as a direct dependency in this milestone.
package artifact

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/xuri/excelize/v2"
)

// XLSXMIME is the canonical MIME type for the .xlsx file format.
const XLSXMIME = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// XLSXRenderer renders an in-memory snapshot to a single .xlsx workbook.
//
// Snapshot shape:
//
//	{
//	  "sheets": [
//	    {
//	      "name": "Topics",
//	      "columns": [
//	        {"header":"Topic","width":40,"validation":["A","B","C"],"conditional":"3color"},
//	        ...
//	      ],
//	      "rows": [[...], [...]],
//	      "per_row_action_url": "https://.../{row_id}",  // optional, {row_id} is substituted
//	      "freeze_header": true                            // optional
//	    }
//	  ]
//	}
//
// Either map[string]any (as decoded from JSON) or the typed XLSXSnapshot
// below is accepted; the typed form is preferred from Go callers.
//
// Schema is stamped onto the produced Artifact descriptor.
type XLSXRenderer struct {
	Schema string
}

// XLSXSnapshot is the typed form of the snapshot accepted by Render.
type XLSXSnapshot struct {
	Sheets []XLSXSheet `json:"sheets"`
}

// XLSXSheet describes one sheet inside the workbook.
type XLSXSheet struct {
	Name            string       `json:"name"`
	Columns         []XLSXColumn `json:"columns"`
	Rows            [][]any      `json:"rows"`
	PerRowActionURL string       `json:"per_row_action_url,omitempty"` // {row_id} substituted with row index (1-based) when no RowID column
	FreezeHeader    bool         `json:"freeze_header,omitempty"`
}

// XLSXColumn describes one column.
type XLSXColumn struct {
	Header      string   `json:"header"`
	Width       float64  `json:"width,omitempty"`       // 0 = default
	Validation  []string `json:"validation,omitempty"`  // dropdown values
	Conditional string   `json:"conditional,omitempty"` // "3color" or "data_bar" or ""
	Marker      bool     `json:"marker,omitempty"`      // emit a star/marker style on truthy cells
}

// Kind reports KindXLSX.
func (r XLSXRenderer) Kind() Kind { return KindXLSX }

// Render assembles the workbook and writes the .xlsx bytes to dst.
func (r XLSXRenderer) Render(_ context.Context, snapshot any, dst io.Writer) (Artifact, error) {
	snap, err := coerceXLSXSnapshot(snapshot)
	if err != nil {
		return Artifact{}, fmt.Errorf("xlsx: %w", err)
	}
	if len(snap.Sheets) == 0 {
		return Artifact{}, fmt.Errorf("xlsx: snapshot has no sheets")
	}

	f := excelize.NewFile()
	// excelize creates a default "Sheet1" — we rename / replace it with the
	// first declared sheet so we don't end up with a dangling empty tab.
	const defaultSheet = "Sheet1"
	defer func() { _ = f.Close() }()

	markerStyle, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Color: "FFB300"},
	})
	if err != nil {
		return Artifact{}, fmt.Errorf("xlsx: build marker style: %w", err)
	}

	for sheetIdx, sheet := range snap.Sheets {
		name := sheet.Name
		if name == "" {
			name = fmt.Sprintf("Sheet%d", sheetIdx+1)
		}

		if sheetIdx == 0 {
			if err := f.SetSheetName(defaultSheet, name); err != nil {
				return Artifact{}, fmt.Errorf("xlsx: rename default sheet: %w", err)
			}
		} else {
			if _, err := f.NewSheet(name); err != nil {
				return Artifact{}, fmt.Errorf("xlsx: new sheet %q: %w", name, err)
			}
		}

		if err := writeSheet(f, name, sheet, markerStyle); err != nil {
			return Artifact{}, err
		}
	}

	// Activate the first sheet so the file opens to the headline data, not
	// to a stale default.
	if idx, err := f.GetSheetIndex(snap.Sheets[0].Name); err == nil {
		f.SetActiveSheet(idx)
	}

	n, err := f.WriteTo(dst)
	if err != nil {
		return Artifact{}, fmt.Errorf("xlsx: write workbook: %w", err)
	}

	return Artifact{
		Kind:      KindXLSX,
		Schema:    r.Schema,
		MIMEType:  XLSXMIME,
		Size:      n,
		CreatedAt: time.Now(),
		Metadata:  map[string]string{"extension": ".xlsx"},
	}, nil
}

// writeSheet populates one sheet: header row, column widths, data rows,
// AutoFilter, conditional formatting, data validation, per-row hyperlinks.
func writeSheet(f *excelize.File, name string, sheet XLSXSheet, markerStyle int) error {
	// Header row.
	for i, col := range sheet.Columns {
		cell, err := excelize.CoordinatesToCellName(i+1, 1)
		if err != nil {
			return fmt.Errorf("xlsx: header cell %d: %w", i, err)
		}
		if err := f.SetCellValue(name, cell, col.Header); err != nil {
			return fmt.Errorf("xlsx: set header %q: %w", col.Header, err)
		}
	}

	// Column widths.
	for i, col := range sheet.Columns {
		if col.Width <= 0 {
			continue
		}
		colName, err := excelize.ColumnNumberToName(i + 1)
		if err != nil {
			return fmt.Errorf("xlsx: column letter %d: %w", i, err)
		}
		if err := f.SetColWidth(name, colName, colName, col.Width); err != nil {
			return fmt.Errorf("xlsx: set width %q: %w", colName, err)
		}
	}

	// Data rows.
	for rIdx, row := range sheet.Rows {
		for cIdx, val := range row {
			cell, err := excelize.CoordinatesToCellName(cIdx+1, rIdx+2)
			if err != nil {
				return fmt.Errorf("xlsx: data cell (%d,%d): %w", rIdx, cIdx, err)
			}
			if err := f.SetCellValue(name, cell, val); err != nil {
				return fmt.Errorf("xlsx: set cell %s: %w", cell, err)
			}
			// Marker style if column requests it and value is truthy.
			if cIdx < len(sheet.Columns) && sheet.Columns[cIdx].Marker && isTruthy(val) {
				if err := f.SetCellStyle(name, cell, cell, markerStyle); err != nil {
					return fmt.Errorf("xlsx: apply marker style: %w", err)
				}
			}
		}
	}

	// AutoFilter across the data region (header + rows), if we have any
	// columns at all.
	if len(sheet.Columns) > 0 {
		lastRow := len(sheet.Rows) + 1
		lastCol, err := excelize.ColumnNumberToName(len(sheet.Columns))
		if err != nil {
			return fmt.Errorf("xlsx: last column letter: %w", err)
		}
		filterRange := fmt.Sprintf("A1:%s%d", lastCol, lastRow)
		if err := f.AutoFilter(name, filterRange, nil); err != nil {
			return fmt.Errorf("xlsx: auto-filter: %w", err)
		}
	}

	// Freeze the header row when requested.
	if sheet.FreezeHeader {
		if err := f.SetPanes(name, &excelize.Panes{
			Freeze:      true,
			YSplit:      1,
			TopLeftCell: "A2",
			ActivePane:  "bottomLeft",
		}); err != nil {
			return fmt.Errorf("xlsx: freeze header: %w", err)
		}
	}

	// Conditional formatting & data validation per column.
	for i, col := range sheet.Columns {
		colLetter, err := excelize.ColumnNumberToName(i + 1)
		if err != nil {
			return fmt.Errorf("xlsx: column letter for cf %d: %w", i, err)
		}
		dataRange := fmt.Sprintf("%s2:%s%d", colLetter, colLetter, len(sheet.Rows)+1)

		if col.Conditional == "3color" && len(sheet.Rows) > 0 {
			if err := f.SetConditionalFormat(name, dataRange, []excelize.ConditionalFormatOptions{{
				Type:     "3_color_scale",
				Criteria: "=",
				MinType:  "min",
				MidType:  "percentile",
				MidValue: "50",
				MaxType:  "max",
				MinColor: "#F8696B",
				MidColor: "#FFEB84",
				MaxColor: "#63BE7B",
			}}); err != nil {
				return fmt.Errorf("xlsx: conditional 3color on %s: %w", dataRange, err)
			}
		}

		if len(col.Validation) > 0 && len(sheet.Rows) > 0 {
			dv := excelize.NewDataValidation(true)
			dv.SetSqref(dataRange)
			if err := dv.SetDropList(col.Validation); err != nil {
				return fmt.Errorf("xlsx: data validation list on %s: %w", dataRange, err)
			}
			if err := f.AddDataValidation(name, dv); err != nil {
				return fmt.Errorf("xlsx: add data validation on %s: %w", dataRange, err)
			}
		}
	}

	// Per-row hyperlinks — applied to column A of each row when an action URL
	// template is supplied. {row_id} is substituted with the row's 1-based
	// index; callers wanting a real id can put it in col A themselves and let
	// the link wrap that cell.
	if sheet.PerRowActionURL != "" {
		for rIdx := range sheet.Rows {
			cell, err := excelize.CoordinatesToCellName(1, rIdx+2)
			if err != nil {
				return fmt.Errorf("xlsx: hyperlink cell %d: %w", rIdx, err)
			}
			url := substituteRowID(sheet.PerRowActionURL, rIdx+1)
			display := "Action"
			if err := f.SetCellHyperLink(name, cell, url, "External", excelize.HyperlinkOpts{
				Display: &display,
			}); err != nil {
				return fmt.Errorf("xlsx: set hyperlink %s: %w", cell, err)
			}
		}
	}

	return nil
}

// coerceXLSXSnapshot normalises the snapshot argument to XLSXSnapshot.
func coerceXLSXSnapshot(snapshot any) (XLSXSnapshot, error) {
	switch s := snapshot.(type) {
	case XLSXSnapshot:
		return s, nil
	case *XLSXSnapshot:
		if s == nil {
			return XLSXSnapshot{}, fmt.Errorf("nil snapshot")
		}
		return *s, nil
	case map[string]any:
		return decodeXLSXSnapshotMap(s)
	default:
		return XLSXSnapshot{}, fmt.Errorf("unsupported snapshot type %T", snapshot)
	}
}

// decodeXLSXSnapshotMap parses the JSON-shaped map form into XLSXSnapshot
// without going through encoding/json (which would force the caller to
// allocate intermediate JSON bytes).
func decodeXLSXSnapshotMap(m map[string]any) (XLSXSnapshot, error) {
	var out XLSXSnapshot
	sheetsAny, ok := m["sheets"]
	if !ok {
		return out, fmt.Errorf("snapshot missing 'sheets'")
	}
	sheets, ok := sheetsAny.([]any)
	if !ok {
		return out, fmt.Errorf("'sheets' must be an array")
	}
	for i, sa := range sheets {
		sm, ok := sa.(map[string]any)
		if !ok {
			return out, fmt.Errorf("sheets[%d] must be an object", i)
		}
		sheet := XLSXSheet{}
		if v, ok := sm["name"].(string); ok {
			sheet.Name = v
		}
		if v, ok := sm["per_row_action_url"].(string); ok {
			sheet.PerRowActionURL = v
		}
		if v, ok := sm["freeze_header"].(bool); ok {
			sheet.FreezeHeader = v
		}
		if cols, ok := sm["columns"].([]any); ok {
			for _, ca := range cols {
				cm, ok := ca.(map[string]any)
				if !ok {
					continue
				}
				col := XLSXColumn{}
				if v, ok := cm["header"].(string); ok {
					col.Header = v
				}
				if v, ok := cm["width"].(float64); ok {
					col.Width = v
				} else if v, ok := cm["width"].(int); ok {
					col.Width = float64(v)
				}
				if v, ok := cm["conditional"].(string); ok {
					col.Conditional = v
				}
				if v, ok := cm["marker"].(bool); ok {
					col.Marker = v
				}
				if vals, ok := cm["validation"].([]any); ok {
					for _, x := range vals {
						if s, ok := x.(string); ok {
							col.Validation = append(col.Validation, s)
						}
					}
				}
				sheet.Columns = append(sheet.Columns, col)
			}
		}
		if rows, ok := sm["rows"].([]any); ok {
			for _, ra := range rows {
				row, ok := ra.([]any)
				if !ok {
					continue
				}
				sheet.Rows = append(sheet.Rows, row)
			}
		}
		out.Sheets = append(out.Sheets, sheet)
	}
	return out, nil
}

// substituteRowID replaces the {row_id} placeholder in the URL template.
// Kept as a small helper so callers can extend it later (e.g. signed tokens
// from APP-503) without touching the renderer loop.
func substituteRowID(template string, rowID int) string {
	out := make([]byte, 0, len(template)+8)
	const placeholder = "{row_id}"
	i := 0
	for i < len(template) {
		if i+len(placeholder) <= len(template) && template[i:i+len(placeholder)] == placeholder {
			out = append(out, []byte(fmt.Sprintf("%d", rowID))...)
			i += len(placeholder)
			continue
		}
		out = append(out, template[i])
		i++
	}
	return string(out)
}

// isTruthy is the loose truthiness check used by the marker-column feature.
// Mirrors what JS and Python users intuit from "if this cell has a value".
func isTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	}
	return true
}
