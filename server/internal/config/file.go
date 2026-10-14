package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// fileValue is a value read from the configuration file.
type fileValue struct {
	value string
	line  int
}

// readFile reads a configuration file of KEY=value lines in UTF-8. The format
// is a simpler form of Docker Compose's env_file and systemd's
// EnvironmentFile:
//
//   - Blank lines and lines starting with # are ignored.
//   - Whitespace around the key and the value is ignored.
//   - A value wrapped in a matching pair of " or ' is unwrapped. Nothing else
//     is unescaped or expanded, and a # after the value is part of it.
//   - An empty value counts as unset.
//
// Every key must be a configuration key and appear at most once. pathKey is
// the setting that named the file, for the problem reported if it cannot be
// read; the map is nil then. Problems never quote a value, because it may be
// a secret, and they name a key only when it has the STORYCREEP_ prefix or is
// a key in other letter case, because text in front of an = may be a stray
// secret too.
func readFile(path, pathKey string, keys []key) (map[string]fileValue, []Problem) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, []Problem{{Key: pathKey, Msg: fmt.Sprintf("cannot read the configuration file: %v", err)}}
	}
	if msg := encodingProblem(b); msg != "" {
		return nil, []Problem{{Key: pathKey, Source: path, Msg: msg}}
	}
	known := map[string]bool{}
	for _, k := range keys {
		known[k.name] = true
	}

	values := map[string]fileValue{}
	firstLine := map[string]int{}
	var problems []Problem
	// Notepad starts UTF-8 files with a byte order mark.
	src := strings.TrimPrefix(string(b), string(rune(0xFEFF)))
	n := 0
	for line := range strings.Lines(src) {
		n++
		where := fmt.Sprintf("%s:%d", path, n)
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || !isKeyName(name) {
			problems = append(problems, Problem{Source: where, Msg: "expected KEY=value"})
			continue
		}
		switch upper := strings.ToUpper(name); {
		case name == fileKey:
			problems = append(problems, Problem{Key: name, Source: where,
				Msg: "names the configuration file, so it cannot be set inside it"})
			continue
		case name != upper && known[upper]:
			problems = append(problems, Problem{Key: name, Source: where,
				Msg: fmt.Sprintf("not a configuration key; key names are case-sensitive, did you mean %s?", upper)})
			continue
		case !strings.HasPrefix(name, "STORYCREEP_"):
			problems = append(problems, Problem{Source: where,
				Msg: "not a configuration key; configuration keys start with STORYCREEP_"})
			continue
		case !known[name]:
			problems = append(problems, Problem{Key: name, Source: where, Msg: "not a configuration key"})
			continue
		case firstLine[name] != 0:
			problems = append(problems, Problem{Key: name, Source: where,
				Msg: fmt.Sprintf("set more than once; first set on line %d", firstLine[name])})
			continue
		}
		firstLine[name] = n
		values[name] = fileValue{value: unquote(strings.TrimSpace(value)), line: n}
	}
	return values, problems
}

// encodingProblem explains why b is not UTF-8 text, or returns "" if it is.
func encodingProblem(b []byte) string {
	switch {
	// Windows PowerShell 5.1 writes UTF-16 with > and Out-File. Its text
	// has a NUL in every ASCII character, and usually a byte order mark.
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}) || bytes.HasPrefix(b, []byte{0xFE, 0xFF}) || bytes.IndexByte(b, 0) >= 0:
		return "the configuration file is not UTF-8 text; it looks like UTF-16, which Windows PowerShell 5.1 " +
			"writes by default. Save it as UTF-8"
	case !utf8.Valid(b):
		return "the configuration file is not UTF-8 text. Save it as UTF-8"
	}
	return ""
}

// isKeyName reports whether s is a non-empty run of letters, digits, and
// underscores.
func isKeyName(s string) bool {
	for _, r := range s {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return s != ""
}

// unquote removes one matching pair of " or ' around v.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}
