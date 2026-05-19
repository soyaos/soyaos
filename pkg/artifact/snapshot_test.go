package artifact

import (
	"regexp"
	"strconv"
	"testing"
)

// hashOrFail is a tiny helper to keep the tests readable.
func hashOrFail(t *testing.T, v any) string {
	t.Helper()
	h, err := CanonicalHash(v)
	if err != nil {
		t.Fatalf("CanonicalHash(%v): %v", v, err)
	}
	return h
}

func TestCanonicalHash_StableUnderKeyOrder(t *testing.T) {
	a := map[string]any{"a": 1, "b": 2}
	b := map[string]any{"b": 2, "a": 1}
	if hashOrFail(t, a) != hashOrFail(t, b) {
		t.Fatalf("hash differed under key-order permutation")
	}
	// Run several times to flush out any reliance on Go's randomised map
	// iteration order (the keys really do come out in different orders
	// across runs of MapKeys, not just across processes).
	for i := 0; i < 32; i++ {
		if hashOrFail(t, a) != hashOrFail(t, b) {
			t.Fatalf("hash differed on iteration %d", i)
		}
	}
}

func TestCanonicalHash_StableForChineseStrings(t *testing.T) {
	v := map[string]any{"标题": "你好世界"}
	first := hashOrFail(t, v)
	second := hashOrFail(t, v)
	if first != second {
		t.Fatalf("Chinese-string hash not stable: %s vs %s", first, second)
	}
	// And a structurally identical fresh map should also match — guards
	// against accidental closure over the original map's internal state.
	third := hashOrFail(t, map[string]any{"标题": "你好世界"})
	if first != third {
		t.Fatalf("Chinese-string hash not stable across fresh map: %s vs %s", first, third)
	}
}

func TestCanonicalHash_FloatStable(t *testing.T) {
	// 0.1 + 0.2 done in IEEE-754 float64 is *exactly* 0.30000000000000004;
	// both expressions then represent the same float64 bit pattern, so the
	// canonical hash must agree. We force runtime evaluation via vars
	// because Go's compiler folds float constant expressions at higher
	// precision, which would mask the rounding.
	var a, b float64 = 0.1, 0.2
	sum := a + b
	lit := 0.30000000000000004
	if sum != lit {
		t.Fatalf("test-precondition: 0.1+0.2 != 0.30000000000000004 (got %v vs %v)", sum, lit)
	}
	if hashOrFail(t, sum) != hashOrFail(t, lit) {
		t.Fatalf("float hashes differed for the same float64 value")
	}
	// Sanity-check the underlying canonical form is the shortest
	// round-trip representation.
	want := strconv.FormatFloat(sum, 'g', -1, 64)
	if want != "0.30000000000000004" {
		t.Fatalf("expected shortest-round-trip form %q, got %q", "0.30000000000000004", want)
	}
}

func TestCanonicalHash_DifferentInputsDifferentHashes(t *testing.T) {
	cases := []struct {
		name string
		a, b any
	}{
		{"different scalar", 1, 2},
		{"different string", "hello", "world"},
		{"different keys", map[string]any{"a": 1}, map[string]any{"b": 1}},
		{"different value at same key", map[string]any{"a": 1}, map[string]any{"a": 2}},
		{"slice vs scalar", []any{1}, 1},
		{"true vs string true", true, "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if hashOrFail(t, tc.a) == hashOrFail(t, tc.b) {
				t.Fatalf("expected different hashes for %v vs %v", tc.a, tc.b)
			}
		})
	}
}

// TestCanonicalHash_RoundTripDecode verifies that a struct and a
// shape-equivalent map[string]any produce the same canonical hash. This
// makes Artifact descriptors robust to "did the producer use a typed
// struct or a generic map?" — the snapshot identity must not care.
//
// Implementation note: this works because encodeStruct emits exported
// field names (or their `json:"..."` tag) sorted lexicographically, which
// is exactly what encodeMap does with a map[string]any of the same shape.
func TestCanonicalHash_RoundTripDecode(t *testing.T) {
	type sample struct {
		Title string
		Count int
	}
	asStruct := sample{Title: "hi", Count: 3}
	asMap := map[string]any{"Title": "hi", "Count": 3}
	if hashOrFail(t, asStruct) != hashOrFail(t, asMap) {
		t.Fatalf("struct and equivalent map hashed differently")
	}
}

func TestCanonicalHash_Length64HexLowercase(t *testing.T) {
	re := regexp.MustCompile(`^[a-f0-9]{64}$`)
	inputs := []any{
		nil,
		0,
		"hello",
		map[string]any{"a": 1, "b": []any{1, 2, 3}},
		[]any{},
		struct{ X int }{X: 7},
	}
	for _, in := range inputs {
		h := hashOrFail(t, in)
		if !re.MatchString(h) {
			t.Fatalf("hash %q does not match ^[a-f0-9]{64}$ (input %v)", h, in)
		}
	}
}

// Bonus sanity-check: pointers and nils canonicalise consistently. Not in
// the spec list but cheap and catches a class of bugs in the reflection
// walker.
func TestCanonicalHash_PointerDereferenceAndNil(t *testing.T) {
	x := 42
	if hashOrFail(t, &x) != hashOrFail(t, 42) {
		t.Fatalf("pointer to int hashed differently from the int")
	}
	var pnil *int
	if hashOrFail(t, pnil) != hashOrFail(t, nil) {
		t.Fatalf("nil *int should hash like untyped nil")
	}
}
