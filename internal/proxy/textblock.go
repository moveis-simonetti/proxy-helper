package proxy

import (
	"os"
	"strings"
)

const (
	blockBegin = "# >>> proxy-helper managed block >>>"
	blockEnd   = "# <<< proxy-helper managed block <<<"
)

// blockMarkers are the two lines that delimit our block in a file.
//
// The comment prefix is not decoration: a marker written with "#" inside a
// JavaScript file is a syntax error, and Firefox answers a syntax error in
// user.js by ignoring the whole file — the settings would silently not
// apply. Each file format gets markers its own parser accepts.
type blockMarkers struct {
	begin string
	end   string
}

// hashMarkers suit shell rc files and anything else where "#" starts a
// comment.
var hashMarkers = blockMarkers{begin: blockBegin, end: blockEnd}

// slashMarkers suit JavaScript, which is what Firefox's user.js is.
var slashMarkers = blockMarkers{
	begin: "// >>> proxy-helper managed block >>>",
	end:   "// <<< proxy-helper managed block <<<",
}

// upsertBlock inserts or replaces the marker-delimited managed block inside
// the file at path, preserving everything else in the file. The file is
// created if it does not exist.
func upsertBlock(path string, body string) ([]byte, error) {
	return upsertBlockWith(path, body, hashMarkers)
}

// upsertBlockWith is upsertBlock with the markers spelled out.
func upsertBlockWith(path string, body string, m blockMarkers) ([]byte, error) {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	block := m.begin + "\n" + strings.TrimRight(body, "\n") + "\n" + m.end

	before, after, found := splitOnBlock(string(existing), m)
	if !found {
		out := string(existing)
		if len(out) > 0 && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		if len(out) > 0 {
			out += "\n"
		}
		out += block + "\n"
		return []byte(out), nil
	}

	return []byte(before + block + after), nil
}

// removeBlock strips the marker-delimited managed block from the file at
// path, if present. Returns the file unchanged if the block is absent.
func removeBlock(path string) ([]byte, bool, error) {
	return removeBlockWith(path, hashMarkers)
}

// removeBlockWith is removeBlock with the markers spelled out.
func removeBlockWith(path string, m blockMarkers) ([]byte, bool, error) {
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	before, after, found := splitOnBlock(string(existing), m)
	if !found {
		return existing, false, nil
	}
	return []byte(before + after), true, nil
}

// readBlock returns the body of the managed block (without markers), if present.
func readBlock(path string) (string, bool, error) {
	return readBlockWith(path, hashMarkers)
}

// readBlockWith is readBlock with the markers spelled out.
func readBlockWith(path string, m blockMarkers) (string, bool, error) {
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	start := strings.Index(string(existing), m.begin)
	end := strings.Index(string(existing), m.end)
	if start == -1 || end == -1 || end < start {
		return "", false, nil
	}
	body := string(existing)[start+len(m.begin) : end]
	return strings.TrimSpace(body), true, nil
}

// splitOnBlock finds the marker block in content and returns the text
// before and after it (with the block's own surrounding blank lines
// trimmed), plus whether it was found.
func splitOnBlock(content string, m blockMarkers) (before, after string, found bool) {
	start := strings.Index(content, m.begin)
	end := strings.Index(content, m.end)
	if start == -1 || end == -1 || end < start {
		return "", "", false
	}
	end += len(m.end)

	before = strings.TrimRight(content[:start], "\n")
	if before != "" {
		before += "\n\n"
	}

	after = strings.TrimLeft(content[end:], "\n")
	if after != "" {
		after = "\n" + after
	}

	return before, after, true
}
