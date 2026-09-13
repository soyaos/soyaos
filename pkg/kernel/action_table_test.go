package kernel

import (
	"github.com/soyaos/soyaos/pkg/soyapack"
	"strings"
	"testing"
)

func TestActionMirrorTable(t *testing.T) {
	cfg := &soyapack.TextValidation{Section: "口播全文", MinChars: 2, MaxChars: 100, MirrorTable: &soyapack.TextMirrorTable{Section: "分镜", Column: "口播", Rows: 3}}
	prefix := "## 口播全文\n报修。受理。修复。\n## 待核实事项\n核实时间\n## 分镜\n"
	valid := "| 时间 | 口播 | 画面 |\n| --- | --- | --- |\n| 0–3 | 报修。 | 图一 |\n| 3–24 | 受理。 | 图二 |\n| 24–30 | 修复。 | 图三 |\n"
	for _, tc := range []struct {
		name, table string
		ok          bool
	}{
		{"exact", valid, true},
		{"escaped pipe in other column", strings.Replace(valid, "图一", `图一\|图二`, 1), true},
		{"no narration column", strings.Replace(valid, "口播", "画面提示", 1), false},
		{"missing row", strings.Replace(valid, "| 24–30 | 修复。 | 图三 |\n", "", 1), false},
		{"wrong words", strings.Replace(valid, "受理。", "估计已经修好。", 1), false},
		{"empty narration", strings.Replace(valid, "受理。", "", 1), false},
		{"no table", "只有画面描述", false},
		{"bad separator", strings.Replace(valid, "---", "bad", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateActionText(prefix+tc.table, cfg); (err == nil) != tc.ok {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
