package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

// settings.json in the VS Code family is JSON with comments that people edit
// by hand. The whole point of editing it in place is that everything the user
// wrote survives, so these tests assert on the surrounding text as much as on
// the values.
const commentedSettings = `{
  // Editor look and feel
  "editor.fontSize": 14,
  "editor.rulers": [80, 120], // soft and hard limits

  /* Terminal
     spans two lines */
  "terminal.integrated.fontSize": 13,
  "files.exclude": {
    "**/.git": true
  }
}
`

func TestStripJSONCCommentsKeepsOffsetsAndParses(t *testing.T) {
	stripped := stripJSONCComments([]byte(commentedSettings))

	if len(stripped) != len(commentedSettings) {
		t.Fatalf("stripping changed the length: %d vs %d", len(stripped), len(commentedSettings))
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(stripped, &doc); err != nil {
		t.Fatalf("stripped document should be valid JSON: %v", err)
	}
	if doc["editor.fontSize"] != float64(14) {
		t.Errorf("editor.fontSize = %v, want 14", doc["editor.fontSize"])
	}
	if strings.Contains(string(stripped), "soft and hard limits") {
		t.Error("comment text survived the strip")
	}
}

func TestStripJSONCCommentsLeavesCommentLikeStringsAlone(t *testing.T) {
	src := `{"a": "http://example.com/x", "b": "/* not a comment */"}`
	var doc map[string]interface{}
	if err := json.Unmarshal(stripJSONCComments([]byte(src)), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc["a"] != "http://example.com/x" {
		t.Errorf("the // inside a URL was treated as a comment: %v", doc["a"])
	}
	if doc["b"] != "/* not a comment */" {
		t.Errorf("comment markers inside a string were stripped: %v", doc["b"])
	}
}

func TestSetJSONCKeyPreservesCommentsAndOrder(t *testing.T) {
	out, err := setJSONCKey([]byte(commentedSettings), "http.proxy", `"http://127.0.0.1:8888"`)
	if err != nil {
		t.Fatalf("setJSONCKey: %v", err)
	}
	got := string(out)

	for _, want := range []string{
		"// Editor look and feel",
		"// soft and hard limits",
		"/* Terminal",
		"spans two lines */",
		`"http.proxy": "http://127.0.0.1:8888"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lost %q:\n%s", want, got)
		}
	}
	// Key order must be untouched: fontSize still precedes rulers.
	if strings.Index(got, "editor.fontSize") > strings.Index(got, "editor.rulers") {
		t.Error("keys were reordered")
	}
	if err := json.Unmarshal(stripJSONCComments(out), &map[string]interface{}{}); err != nil {
		t.Fatalf("result is not valid JSONC: %v", err)
	}
}

func TestSetJSONCKeyReplacesInPlace(t *testing.T) {
	src := []byte(`{
  "http.proxy": "http://old:3128", // keep me
  "editor.fontSize": 14
}
`)
	out, err := setJSONCKey(src, "http.proxy", `"http://new:8888"`)
	if err != nil {
		t.Fatalf("setJSONCKey: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "old:3128") {
		t.Error("old value survived")
	}
	if !strings.Contains(got, "// keep me") {
		t.Error("the trailing comment was destroyed")
	}
	if strings.Count(got, "http.proxy") != 1 {
		t.Errorf("key was duplicated instead of replaced:\n%s", got)
	}
}

func TestSetJSONCKeyOnEmptyObject(t *testing.T) {
	out, err := setJSONCKey([]byte("{}\n"), "http.proxy", `"http://127.0.0.1:8888"`)
	if err != nil {
		t.Fatalf("setJSONCKey: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, out)
	}
	if doc["http.proxy"] != "http://127.0.0.1:8888" {
		t.Errorf("got %v", doc["http.proxy"])
	}
}

func TestRemoveJSONCKeysLeavesValidJSON(t *testing.T) {
	src := []byte(commentedSettings)
	var err error
	for _, kv := range [][2]string{
		{"http.proxy", `"http://127.0.0.1:8888"`},
		{"http.proxyStrictSSL", "false"},
		{"http.noProxy", `["localhost"]`},
	} {
		if src, err = setJSONCKey(src, kv[0], kv[1]); err != nil {
			t.Fatalf("setJSONCKey %s: %v", kv[0], err)
		}
	}

	out, err := removeJSONCKeys(src, "http.proxy", "http.proxyStrictSSL", "http.noProxy")
	if err != nil {
		t.Fatalf("removeJSONCKeys: %v", err)
	}
	got := string(out)

	for _, gone := range []string{"http.proxy", "http.proxyStrictSSL", "http.noProxy"} {
		if strings.Contains(got, gone) {
			t.Errorf("%s survived removal:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"// Editor look and feel", "editor.fontSize", "files.exclude"} {
		if !strings.Contains(got, kept) {
			t.Errorf("removal collateral: lost %q\n%s", kept, got)
		}
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(stripJSONCComments(out), &doc); err != nil {
		t.Fatalf("result is not valid JSONC after removal: %v\n%s", err, got)
	}
	if len(doc) != 4 {
		t.Errorf("expected the 4 original keys to remain, got %d: %v", len(doc), doc)
	}
}

// TestRemoveJSONCKeysHandlesTheLastMember covers the comma bookkeeping: a key
// removed from the end must take the preceding comma with it, or the document
// is left with a dangling comma and stops parsing.
func TestRemoveJSONCKeysHandlesTheLastMember(t *testing.T) {
	src := []byte("{\n  \"a\": 1,\n  \"http.proxy\": \"x\"\n}\n")
	out, err := removeJSONCKeys(src, "http.proxy")
	if err != nil {
		t.Fatalf("removeJSONCKeys: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("dangling comma left behind: %v\n%s", err, out)
	}
	if len(doc) != 1 || doc["a"] != float64(1) {
		t.Errorf("got %v", doc)
	}
}

func TestRemoveJSONCKeysHandlesTheOnlyMember(t *testing.T) {
	out, err := removeJSONCKeys([]byte("{\n  \"http.proxy\": \"x\"\n}\n"), "http.proxy")
	if err != nil {
		t.Fatalf("removeJSONCKeys: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, out)
	}
	if len(doc) != 0 {
		t.Errorf("expected an empty object, got %v", doc)
	}
}

func TestScanJSONCObjectRefusesGarbage(t *testing.T) {
	for _, src := range []string{
		`[1, 2]`,         // not an object
		`{"a": }`,        // missing value
		`{"a" 1}`,        // missing colon
		`{"a": {"b": 1}`, // unbalanced
	} {
		if _, err := scanJSONCObject([]byte(src)); err == nil {
			t.Errorf("expected an error for %q — writing to a file we cannot parse would corrupt it", src)
		}
	}
}

// Trailing commas are the other dialect settings.json files come in: editors
// tolerate them, encoding/json does not, and a real Cursor config in the wild
// had one.
func TestStripJSONCHandlesTrailingCommas(t *testing.T) {
	src := `{
  "a": [1, 2,],
  "b": {"c": true,},
  "d": 1,
}`
	var doc map[string]interface{}
	if err := json.Unmarshal(stripJSONC([]byte(src)), &doc); err != nil {
		t.Fatalf("trailing commas should be tolerated: %v", err)
	}
	if len(doc) != 3 {
		t.Errorf("got %d keys, want 3: %v", len(doc), doc)
	}
	if !strings.Contains(string(stripJSONC([]byte(`{"x": "a,}"}`))), `"a,}"`) {
		t.Error("a comma inside a string must not be treated as trailing")
	}
}

func TestSetJSONCKeyOnDocumentWithTrailingComma(t *testing.T) {
	src := []byte("{\n  \"a\": 1,\n}\n")
	out, err := setJSONCKey(src, "http.proxy", `"http://127.0.0.1:8888"`)
	if err != nil {
		t.Fatalf("setJSONCKey: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(stripJSONC(out), &doc); err != nil {
		t.Fatalf("result should still parse: %v\n%s", err, out)
	}
	if doc["http.proxy"] != "http://127.0.0.1:8888" || doc["a"] != float64(1) {
		t.Errorf("got %v", doc)
	}
}

// TestRemoveJSONCKeysKeepsTheClosingBraceOnItsOwnLine guards a regression
// found against a real Cursor settings.json: removing the last member used to
// swallow the newline that belongs to the closing brace, pulling '}' up onto
// the previous line and reformatting a file the user never asked to reformat.
func TestRemoveJSONCKeysKeepsTheClosingBraceOnItsOwnLine(t *testing.T) {
	original := "{\n  \"a\": 1,\n  \"b\": true\n}\n"

	withKey, err := setJSONCKey([]byte(original), "http.proxy", `"http://127.0.0.1:8888"`)
	if err != nil {
		t.Fatalf("setJSONCKey: %v", err)
	}
	out, err := removeJSONCKeys(withKey, "http.proxy")
	if err != nil {
		t.Fatalf("removeJSONCKeys: %v", err)
	}

	if string(out) != original {
		t.Errorf("set followed by remove must restore the file byte for byte:\nwant %q\ngot  %q", original, out)
	}
}
