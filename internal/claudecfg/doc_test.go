package claudecfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseDocUsesNumberAndHash(t *testing.T) {
	doc, err := ParseDoc([]byte(`{"n":9007199254740993,"env":{"A":"1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	n, ok := doc.Raw["n"].(json.Number)
	if !ok || n.String() != "9007199254740993" {
		t.Fatalf("n = %#v", doc.Raw["n"])
	}
	if doc.Hash == "" {
		t.Fatal("empty hash")
	}
	if !reflect.DeepEqual(doc.Raw, doc.Original) {
		t.Fatal("original must equal raw")
	}
}

func TestParseDocRejectsCommentsAndTrailingComma(t *testing.T) {
	cases := []string{
		"{\n  // comment\n  \"a\": 1\n}",
		"{\"a\":1,}",
	}
	for _, src := range cases {
		_, err := ParseDoc([]byte(src))
		if err == nil {
			t.Fatalf("expected error for %q", src)
		}
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Fatalf("error type = %T", err)
		}
		if pe.Line < 1 || pe.Col < 1 {
			t.Fatalf("line/col = %d/%d", pe.Line, pe.Col)
		}
	}
}

func TestParseDocRequiresObject(t *testing.T) {
	if _, err := ParseDoc([]byte(`[]`)); err == nil {
		t.Fatal("expected root array error")
	}
	if _, err := ParseDoc(bytes.Repeat([]byte(" "), maxDocBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("error = %v", err)
	}
}

func TestMarshalDocUsesTwoSpacesAndNewline(t *testing.T) {
	doc, err := ParseDoc([]byte(`{"b":2,"a":{"c":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := doc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": {\n    \"c\": 1\n  },\n  \"b\": 2\n}\n"
	if string(b) != want {
		t.Fatalf("marshal = %q", b)
	}
}

func TestDiffReturnsLeafPaths(t *testing.T) {
	original := map[string]any{
		"model":       "a",
		"permissions": map[string]any{"allow": []any{"Bash"}},
		"env":         map[string]any{"A": "1", "B": "2"},
	}
	current := map[string]any{
		"model":       "b",
		"permissions": map[string]any{"allow": []any{"Bash", "Read"}},
		"env":         map[string]any{"A": "1"},
	}
	got := Diff(original, current)
	want := []string{"env.B", "model", "permissions.allow"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diff = %v, want %v", got, want)
	}
}

func TestApplyPatchOnlyChangesTargets(t *testing.T) {
	base := map[string]any{
		"model": "a",
		"keep":  map[string]any{"x": json.Number("1")},
		"env":   map[string]any{"A": "1", "B": "2"},
	}
	current := map[string]any{
		"model": "b",
		"env":   map[string]any{"A": "1"},
	}
	got := ApplyPatch(base, []string{"model", "env.B"}, current)
	if got["model"] != "b" {
		t.Fatalf("model = %#v", got["model"])
	}
	if _, ok := Get(got, "env.B"); ok {
		t.Fatal("env.B not deleted")
	}
	if v, ok := Get(got, "keep.x"); !ok || v.(json.Number).String() != "1" {
		t.Fatalf("keep.x = %#v", v)
	}
}

func TestGetSetDelete(t *testing.T) {
	root := map[string]any{}
	Set(root, "a.b.c", "v")
	if v, ok := Get(root, "a.b.c"); !ok || v != "v" {
		t.Fatalf("get = %#v", v)
	}
	Delete(root, "a.b.c")
	if _, ok := Get(root, "a.b.c"); ok {
		t.Fatal("delete failed")
	}
}

func TestParseErrorMessageContainsPosition(t *testing.T) {
	_, err := ParseDoc([]byte("{\n  \"a\": 1,\n}"))
	if err == nil || !strings.Contains(err.Error(), "行") {
		t.Fatalf("error = %v", err)
	}
}
