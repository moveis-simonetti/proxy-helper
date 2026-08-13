package proxy

import (
	"os"
	"strings"
)

const (
	blockBegin = "# >>> proxy-helper managed block >>>"
	blockEnd   = "# <<< proxy-helper managed block <<<"
)

// upsertBlock inserts or replaces the marker-delimited managed block inside
// the file at path, preserving everything else in the file. The file is
// created if it does not exist.
func upsertBlock(path string, body string) ([]byte, error) {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	block := blockBegin + "\n" + strings.TrimRight(body, "\n") + "\n" + blockEnd

	before, after, found := splitOnBlock(string(existing))
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
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	before, after, found := splitOnBlock(string(existing))
	if !found {
		return existing, false, nil
	}
	return []byte(before + after), true, nil
}

// readBlock returns the body of the managed block (without markers), if present.
func readBlock(path string) (string, bool, error) {
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	start := strings.Index(string(existing), blockBegin)
	end := strings.Index(string(existing), blockEnd)
	if start == -1 || end == -1 || end < start {
		return "", false, nil
	}
	body := string(existing)[start+len(blockBegin) : end]
	return strings.TrimSpace(body), true, nil
}

// splitOnBlock finds the marker block in content and returns the text
// before and after it (with the block's own surrounding blank lines
// trimmed), plus whether it was found.
func splitOnBlock(content string) (before, after string, found bool) {
	start := strings.Index(content, blockBegin)
	end := strings.Index(content, blockEnd)
	if start == -1 || end == -1 || end < start {
		return "", "", false
	}
	end += len(blockEnd)

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
