package artifact

// CanonicalHash produces a stable sha256 over the *data snapshot* used to
// render one or more Artifacts. The hash is the cross-form proof that
// "HTML / long_image / PDF were rendered from the same source", without
// depending on renderer byte-determinism (fonts, Chrome version, etc.).
//
// This implements the snapshot-identity side of the proposed DD-017 framing
// (see flagship roadmap §"NewsBeam · 反向倒逼 #4").
//
// Canonicalisation rules:
//
//   - JSON-shaped output: objects, arrays, strings, numbers, booleans, null.
//   - Object keys are sorted lexicographically (by raw UTF-8 byte order) at
//     every nesting level.
//   - No insignificant whitespace; LF line endings only (we don't actually
//     emit any newlines, so this falls out naturally).
//   - Floats use the shortest round-trip form via
//     strconv.FormatFloat(v, 'g', -1, 64). Integer-valued floats are written
//     without a fractional part.
//   - Pointers are dereferenced before encoding; nil pointers / nil
//     interfaces / nil maps / nil slices all encode as JSON null. Slices and
//     arrays are encoded by their element values, never by Go type metadata,
//     so []int{1,2,3} and []any{1,2,3} hash identically.
//
// Notes on Unicode normalisation:
//
//   - The spec asks for UTF-8 NFC normalisation of all string values. That
//     would require golang.org/x/text/unicode/norm, which is NOT in this
//     module's go.mod. Per the APP-464 constraints we therefore SKIP NFC
//     here and keep the implementation stdlib-only. Practical impact: two
//     snapshots that contain the same logical string in different Unicode
//     normalisation forms (e.g. NFC vs NFD) will hash differently. This is
//     acceptable for the alpha; revisit once we are willing to take a
//     dependency on x/text.
//
// The function is deterministic across map iteration order, pointer
// identity, and slice element types (Go-level type metadata is never part
// of the canonical bytes).

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// CanonicalHash returns a 64-character lowercase hex sha256 of the canonical
// JSON encoding of snapshot.
//
// It returns an error if snapshot contains a value that cannot be canonically
// encoded (channels, functions, complex numbers, NaN/±Inf, or non-string map
// keys whose stringification would be ambiguous).
func CanonicalHash(snapshot any) (string, error) {
	var buf strings.Builder
	if err := encodeCanonical(&buf, reflect.ValueOf(snapshot)); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(buf.String()))
	return hex.EncodeToString(sum[:]), nil
}

// errUnsupportedKind is returned for Go kinds that have no canonical JSON
// representation.
var errUnsupportedKind = errors.New("artifact: unsupported kind in snapshot")

// encodeCanonical writes the canonical JSON encoding of v into w.
//
// w is a *strings.Builder rather than io.Writer to keep allocation low and
// avoid having to deal with partial-write errors in deeply nested calls.
func encodeCanonical(w *strings.Builder, v reflect.Value) error {
	// Unwrap interfaces and pointers; both nil-pointer and nil-interface
	// become JSON null.
	for {
		if !v.IsValid() {
			w.WriteString("null")
			return nil
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				w.WriteString("null")
				return nil
			}
			v = v.Elem()
			continue
		}
		break
	}

	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			w.WriteString("true")
		} else {
			w.WriteString("false")
		}
		return nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		w.WriteString(strconv.FormatInt(v.Int(), 10))
		return nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		w.WriteString(strconv.FormatUint(v.Uint(), 10))
		return nil

	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("artifact: cannot canonicalise non-finite float %v", f)
		}
		w.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
		return nil

	case reflect.String:
		encodeJSONString(w, v.String())
		return nil

	case reflect.Slice, reflect.Array:
		// A nil slice encodes as null; an empty (but non-nil) slice encodes
		// as []. This mirrors encoding/json and keeps "missing" vs "empty"
		// distinguishable in the hash.
		if v.Kind() == reflect.Slice && v.IsNil() {
			w.WriteString("null")
			return nil
		}
		// Special-case []byte → base16-ish? No: we treat []byte like any
		// other []uint8 so the hash is purely structural. Callers who want
		// to embed binary blobs should base64-encode first.
		w.WriteByte('[')
		n := v.Len()
		for i := 0; i < n; i++ {
			if i > 0 {
				w.WriteByte(',')
			}
			if err := encodeCanonical(w, v.Index(i)); err != nil {
				return err
			}
		}
		w.WriteByte(']')
		return nil

	case reflect.Map:
		if v.IsNil() {
			w.WriteString("null")
			return nil
		}
		return encodeMap(w, v)

	case reflect.Struct:
		return encodeStruct(w, v)

	case reflect.Chan, reflect.Func, reflect.UnsafePointer, reflect.Complex64, reflect.Complex128:
		return fmt.Errorf("%w: %s", errUnsupportedKind, v.Kind())

	default:
		return fmt.Errorf("%w: %s", errUnsupportedKind, v.Kind())
	}
}

// encodeMap emits a canonical JSON object. Keys are stringified (only
// string-kind keys are accepted in the alpha — see TODO below) and sorted
// lexicographically by the resulting UTF-8 bytes.
func encodeMap(w *strings.Builder, v reflect.Value) error {
	if v.Type().Key().Kind() != reflect.String {
		// Stringifying arbitrary key types (int, struct, ...) is ambiguous:
		// fmt.Sprint can collide (e.g. true and "true"). Reject for now.
		return fmt.Errorf("artifact: map key kind %s not supported (must be string)", v.Type().Key().Kind())
	}
	keys := make([]string, 0, v.Len())
	for _, k := range v.MapKeys() {
		keys = append(keys, k.String())
	}
	sort.Strings(keys)

	w.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			w.WriteByte(',')
		}
		encodeJSONString(w, k)
		w.WriteByte(':')
		// Re-fetch via the string key to handle the case where the map's
		// key type is a named string type.
		mv := v.MapIndex(reflect.ValueOf(k).Convert(v.Type().Key()))
		if err := encodeCanonical(w, mv); err != nil {
			return err
		}
	}
	w.WriteByte('}')
	return nil
}

// encodeStruct emits a struct as a canonical JSON object using exported
// field names (or `json:"..."` tags when present). This makes
// struct-vs-map equivalence achievable: a struct {A int; B int} and a
// map[string]any{"A": ..., "B": ...} hash identically.
//
// Tag handling:
//   - `json:"-"` skips the field entirely.
//   - `json:"name,omitempty"` uses "name"; omitempty is honoured for
//     zero values so callers can keep struct/map parity by leaving a
//     field unset rather than setting it to the zero value.
//   - Anonymous (embedded) struct fields are inlined.
func encodeStruct(w *strings.Builder, v reflect.Value) error {
	var fields []structField
	collectStructFields(v, &fields)
	sort.Slice(fields, func(i, j int) bool { return fields[i].name < fields[j].name })

	w.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			w.WriteByte(',')
		}
		encodeJSONString(w, f.name)
		w.WriteByte(':')
		if err := encodeCanonical(w, f.val); err != nil {
			return err
		}
	}
	w.WriteByte('}')
	return nil
}

// structField is the intermediate field descriptor used by encodeStruct.
type structField struct {
	name string
	val  reflect.Value
}

func collectStructFields(v reflect.Value, out *[]structField) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		tag := sf.Tag.Get("json")
		name := sf.Name
		omitempty := false
		if tag != "" {
			parts := strings.Split(tag, ",")
			if parts[0] == "-" {
				continue
			}
			if parts[0] != "" {
				name = parts[0]
			}
			for _, p := range parts[1:] {
				if p == "omitempty" {
					omitempty = true
				}
			}
		}
		fv := v.Field(i)
		if sf.Anonymous && fv.Kind() == reflect.Struct {
			collectStructFields(fv, out)
			continue
		}
		if omitempty && isZero(fv) {
			continue
		}
		*out = append(*out, structField{name: name, val: fv})
	}
}

func isZero(v reflect.Value) bool {
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	}
	return v.IsZero()
}

// encodeJSONString writes s as a canonical JSON string literal.
// We follow RFC 8259 §7 with the minimum-escape choice (only the mandatory
// characters are escaped, so non-ASCII UTF-8 passes through verbatim).
func encodeJSONString(w *strings.Builder, s string) {
	w.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			w.WriteString(`\"`)
		case '\\':
			w.WriteString(`\\`)
		case '\n':
			w.WriteString(`\n`)
		case '\r':
			w.WriteString(`\r`)
		case '\t':
			w.WriteString(`\t`)
		case '\b':
			w.WriteString(`\b`)
		case '\f':
			w.WriteString(`\f`)
		default:
			if c < 0x20 {
				// Other C0 controls → \u00XX.
				const hexdigits = "0123456789abcdef"
				w.WriteString(`\u00`)
				w.WriteByte(hexdigits[c>>4])
				w.WriteByte(hexdigits[c&0xF])
			} else {
				w.WriteByte(c)
			}
		}
	}
	w.WriteByte('"')
}
