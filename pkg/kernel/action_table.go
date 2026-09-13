package kernel

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/soyaos/soyaos/pkg/soyapack"
)

func validateActionMirrorTable(content, source string, cfg *soyapack.TextMirrorTable) error {
	var section []string
	active, found := false, false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			if active {
				break
			}
			if strings.TrimSpace(strings.TrimLeft(trimmed, "#")) == cfg.Section {
				active, found = true, true
			}
			continue
		}
		if active && trimmed != "" {
			section = append(section, trimmed)
		}
	}
	if !found || len(section) < 3 {
		return fmt.Errorf("section %q requires a Markdown table with %d rows and a %q column containing the original narration", cfg.Section, cfg.Rows, cfg.Column)
	}
	header := splitActionTableRow(section[0])
	separator := splitActionTableRow(section[1])
	column := -1
	for i, h := range header {
		if h == cfg.Column {
			if column >= 0 {
				return fmt.Errorf("duplicate mirror table column %q", cfg.Column)
			}
			column = i
		}
	}
	if column < 0 || len(separator) != len(header) {
		return fmt.Errorf("mirror table missing column %q or valid separator", cfg.Column)
	}
	for _, cell := range separator {
		if len(strings.Trim(cell, ":")) < 3 || strings.Trim(cell, ":-") != "" {
			return fmt.Errorf("invalid mirror table separator")
		}
	}
	if len(section)-2 != cfg.Rows {
		return fmt.Errorf("mirror table requires exactly %d data rows, got %d", cfg.Rows, len(section)-2)
	}
	var parts []string
	for _, line := range section[2:] {
		cells := splitActionTableRow(line)
		if len(cells) != len(header) || cells[column] == "" {
			return fmt.Errorf("mirror table row requires a nonempty %q cell", cfg.Column)
		}
		parts = append(parts, cells[column])
	}
	compact := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s)
	}
	if compact(strings.Join(parts, "")) != compact(source) {
		return fmt.Errorf("mirror table %q cells must concatenate to the source section verbatim; copy the narration into the table without omissions or new words", cfg.Column)
	}
	return nil
}

func splitActionTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, `\|`) {
		line = strings.TrimSuffix(line, "|")
	}
	var cells []string
	var cell strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' && i+1 < len(line) && line[i+1] == '|' {
			cell.WriteByte('|')
			i++
			continue
		}
		if line[i] == '|' {
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
			continue
		}
		cell.WriteByte(line[i])
	}
	return append(cells, strings.TrimSpace(cell.String()))
}
