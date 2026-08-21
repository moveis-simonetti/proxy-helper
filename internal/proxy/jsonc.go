package proxy

import (
	"fmt"
	"strings"
)

// JSONC — JSON with comments — is what editors in the VS Code family write
// for their user settings, and what users hand-edit afterwards. Parsing such
// a file into a map and marshalling it back would drop every comment and
// reorder every key, so the proxy keys are edited in place instead: only the
// bytes belonging to those keys change, and the rest of the file — comments,
// key order, indentation, blank lines — survives untouched.

// stripJSONC blanks out the two things JSONC allows and encoding/json does
// not: comments and trailing commas. Offending bytes become spaces rather than
// being removed, so every offset in the result still refers to the same byte in
// the original — which is what lets the scanner and the parser agree.
func stripJSONC(src []byte) []byte {
	out := stripJSONCComments(src)
	// A comma is trailing when the next meaningful byte closes the container.
	for i := 0; i < len(out); {
		if out[i] == '"' {
			i = skipString(out, i)
			continue
		}
		if out[i] == ',' {
			if j := skipBlanks(out, i+1); j < len(out) && (out[j] == '}' || out[j] == ']') {
				out[i] = ' '
			}
		}
		i++
	}
	return out
}

// stripJSONCComments blanks out comments so a strict JSON parser can read the
// document. Comment bytes are replaced by spaces rather than removed, so every
// offset in the result still refers to the same byte in the original.
func stripJSONCComments(src []byte) []byte {
	out := make([]byte, len(src))
	copy(out, src)

	for i := 0; i < len(src); {
		switch {
		case src[i] == '"':
			i = skipString(src, i)
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				out[i] = ' '
				i++
			}
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '*':
			for i < len(src) {
				end := src[i] == '*' && i+1 < len(src) && src[i+1] == '/'
				if src[i] != '\n' { // keep newlines so line numbers still match
					out[i] = ' '
				}
				i++
				if end {
					out[i] = ' '
					i++
					break
				}
			}
		default:
			i++
		}
	}
	return out
}

// skipString returns the index just past the string starting at i (which must
// be the opening quote), honouring backslash escapes.
func skipString(src []byte, i int) int {
	i++ // opening quote
	for i < len(src) {
		if src[i] == '\\' {
			i += 2
			continue
		}
		if src[i] == '"' {
			return i + 1
		}
		i++
	}
	return i
}

// jsoncMember is one key/value pair of the top-level object, with the byte
// spans needed to rewrite or delete it without touching anything else.
type jsoncMember struct {
	key        string
	valueStart int // first byte of the value
	valueEnd   int // one past the last byte of the value
	start      int // first byte of the member (the key's opening quote)
	commaEnd   int // one past the comma following this member, or valueEnd
}

// jsoncObject is a scanned top-level object.
type jsoncObject struct {
	members   []jsoncMember
	bodyStart int // one past the '{'
	closeAt   int // index of the '}'
}

// scanJSONCObject walks the top-level object recording where each member and
// its value begin and end. It is deliberately lenient about comments and
// whitespace, and strict about structure: anything it cannot make sense of is
// an error, so the caller can refuse to write rather than corrupt the file.
func scanJSONCObject(src []byte) (*jsoncObject, error) {
	i := skipBlanks(src, 0)
	if i >= len(src) || src[i] != '{' {
		return nil, fmt.Errorf("expected a JSON object at the top level")
	}
	obj := &jsoncObject{bodyStart: i + 1}
	i++

	for {
		i = skipBlanks(src, i)
		if i >= len(src) {
			return nil, fmt.Errorf("unexpected end of file inside the object")
		}
		if src[i] == '}' {
			obj.closeAt = i
			return obj, nil
		}
		if src[i] != '"' {
			return nil, fmt.Errorf("expected a quoted key at byte %d", i)
		}

		m := jsoncMember{start: i}
		keyEnd := skipString(src, i)
		unquoted, err := unquoteJSONString(src[i:keyEnd])
		if err != nil {
			return nil, err
		}
		m.key = unquoted

		i = skipBlanks(src, keyEnd)
		if i >= len(src) || src[i] != ':' {
			return nil, fmt.Errorf("expected ':' after key %q", m.key)
		}
		i = skipBlanks(src, i+1)

		m.valueStart = i
		i, err = skipValue(src, i)
		if err != nil {
			return nil, err
		}
		m.valueEnd = i
		m.commaEnd = i

		i = skipBlanks(src, i)
		if i < len(src) && src[i] == ',' {
			i++
			m.commaEnd = i
		}
		obj.members = append(obj.members, m)
	}
}

// find returns the member with the given key, if present.
func (o *jsoncObject) find(key string) (jsoncMember, bool) {
	for _, m := range o.members {
		if m.key == key {
			return m, true
		}
	}
	return jsoncMember{}, false
}

// skipBlanks advances past whitespace and comments.
func skipBlanks(src []byte, i int) int {
	for i < len(src) {
		switch {
		case src[i] == ' ' || src[i] == '\t' || src[i] == '\n' || src[i] == '\r':
			i++
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
		default:
			return i
		}
	}
	return i
}

// skipValue returns the index just past the value starting at i.
func skipValue(src []byte, i int) (int, error) {
	if i >= len(src) {
		return i, fmt.Errorf("unexpected end of file where a value was expected")
	}
	switch src[i] {
	case '"':
		return skipString(src, i), nil
	case '{', '[':
		return skipContainer(src, i)
	default:
		// number, true, false, null — runs until a structural byte.
		start := i
		for i < len(src) && !strings.ContainsRune(",}]", rune(src[i])) &&
			src[i] != ' ' && src[i] != '\t' && src[i] != '\n' && src[i] != '\r' {
			i++
		}
		if i == start {
			return i, fmt.Errorf("empty value at byte %d", start)
		}
		return i, nil
	}
}

// skipContainer returns the index just past the object or array starting at i,
// tracking nesting while ignoring braces that appear inside strings or comments.
func skipContainer(src []byte, i int) (int, error) {
	depth := 0
	for i < len(src) {
		i = skipBlanks(src, i)
		if i >= len(src) {
			break
		}
		switch src[i] {
		case '"':
			i = skipString(src, i)
			continue
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1, nil
			}
		}
		i++
	}
	return i, fmt.Errorf("unbalanced object or array")
}

// unquoteJSONString decodes a quoted JSON string. Only the escapes that can
// appear in a settings key are handled; anything else is returned verbatim,
// which is enough because keys are compared against literals we control.
func unquoteJSONString(quoted []byte) (string, error) {
	if len(quoted) < 2 || quoted[0] != '"' || quoted[len(quoted)-1] != '"' {
		return "", fmt.Errorf("malformed key %q", string(quoted))
	}
	body := string(quoted[1 : len(quoted)-1])
	r := strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\/`, `/`, `\n`, "\n", `\t`, "\t")
	return r.Replace(body), nil
}

// setJSONCKey replaces the value of key with encoded (already-marshalled
// JSON), or appends the member if the key is absent. Everything else in the
// document is preserved byte for byte.
func setJSONCKey(src []byte, key, encoded string) ([]byte, error) {
	obj, err := scanJSONCObject(src)
	if err != nil {
		return nil, err
	}

	if m, ok := obj.find(key); ok {
		out := make([]byte, 0, len(src)+len(encoded))
		out = append(out, src[:m.valueStart]...)
		out = append(out, encoded...)
		out = append(out, src[m.valueEnd:]...)
		return out, nil
	}

	indent := detectIndent(src, obj)
	member := fmt.Sprintf("%s%q: %s", indent, key, encoded)

	// Insert as the last member, adding a comma to the previous one when the
	// object is not empty.
	insertAt := obj.closeAt
	prefix := "\n"
	if n := len(obj.members); n > 0 {
		last := obj.members[n-1]
		insertAt = last.commaEnd
		if last.commaEnd == last.valueEnd { // no trailing comma yet
			prefix = ",\n"
		}
	}

	out := make([]byte, 0, len(src)+len(member)+2)
	out = append(out, src[:insertAt]...)
	out = append(out, prefix...)
	out = append(out, member...)
	out = append(out, src[insertAt:]...)
	return out, nil
}

// removeJSONCKeys deletes the given keys along with their separating comma,
// leaving the rest of the document untouched. Absent keys are ignored.
func removeJSONCKeys(src []byte, keys ...string) ([]byte, error) {
	for _, key := range keys {
		obj, err := scanJSONCObject(src)
		if err != nil {
			return nil, err
		}
		m, ok := obj.find(key)
		if !ok {
			continue
		}

		start, end := m.start, m.commaEnd
		tookPrecedingComma := false
		if m.commaEnd == m.valueEnd {
			// Last member: take the preceding comma with it, so the member
			// before this one does not end up with a dangling comma.
			if prev := previousMember(obj, key); prev != nil {
				start = prev.valueEnd
				end = m.valueEnd
				tookPrecedingComma = true
			}
		}
		if !tookPrecedingComma {
			// Swallow the rest of the line, so the member does not leave a
			// blank one behind. When the preceding comma was taken instead,
			// the newline after this member is the one that belongs to the
			// closing brace — eating it would pull '}' up a line.
			for end < len(src) && (src[end] == ' ' || src[end] == '\t') {
				end++
			}
			if end < len(src) && src[end] == '\n' {
				end++
			}
			for start > 0 && (src[start-1] == ' ' || src[start-1] == '\t') {
				start--
			}
		}

		out := make([]byte, 0, len(src))
		out = append(out, src[:start]...)
		out = append(out, src[end:]...)
		src = out
	}
	return src, nil
}

// previousMember returns the member preceding key, or nil if it is the first.
func previousMember(obj *jsoncObject, key string) *jsoncMember {
	for i, m := range obj.members {
		if m.key == key {
			if i == 0 {
				return nil
			}
			return &obj.members[i-1]
		}
	}
	return nil
}

// detectIndent reports the indentation of the object's first member, so an
// inserted key lines up with the ones already there.
func detectIndent(src []byte, obj *jsoncObject) string {
	if len(obj.members) == 0 {
		return "  "
	}
	start := obj.members[0].start
	lineStart := start
	for lineStart > 0 && src[lineStart-1] != '\n' {
		lineStart--
	}
	indent := src[lineStart:start]
	for _, b := range indent {
		if b != ' ' && b != '\t' {
			return "  "
		}
	}
	if len(indent) == 0 {
		return "  "
	}
	return string(indent)
}
