package node

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/CyberCute-Studio/storycreep/server/internal/input"
)

// Limits of a node's tags, which the database holds them to as well
// (migration 00023), so that the undo of a change of them, which sets the
// stored ones back, holds none that the payload's schema refuses: change both
// at once.
const (
	// MaxTags is the most tags that a node may have.
	MaxTags = 32
	// MaxTagLength is the most characters that a tag may have.
	MaxTagLength = 50
	// MaxTagQueryLength is the most characters that the start of a tag that
	// the tags are suggested for may have.
	MaxTagQueryLength = MaxTagLength
	// MaxSuggestions is the most tags that a suggestion holds.
	MaxSuggestions = 100
)

// fold returns the key by which two tags are the same tag: the tag in
// Unicode's normalization form C, with its case folded, so that Quest, QUEST,
// and quest are one tag (PRD FR-EDIT-06, plan note N43), and an accented
// letter written as one character or as a letter and a mark is one letter.
// The folded tag is put in form C again, since folding may leave a letter in
// another form: ΐ folds to ι with two marks, and Ϊ́, a capital and a mark,
// to ϊ with one, which form C writes alike, as ΐ.
func fold(tag string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(tag)))
}

// checkTags returns the problems of a node's tags at the field at: at most
// MaxTags, each a name of 1 to MaxTagLength characters, without white space
// around it, and without control characters (input.CheckTrimmedName), and no
// two the same tag but for their case.
func checkTags(at string, tags []string) input.FieldErrors {
	if len(tags) > MaxTags {
		return field(at, fmt.Sprintf("holds more than %d tags", MaxTags))
	}
	var errs input.FieldErrors
	first := map[string]int{}
	for i, tag := range tags {
		f := fmt.Sprintf("%s[%d]", at, i)
		if problem := input.CheckTrimmedName(tag, MaxTagLength); problem != "" {
			errs = append(errs, input.FieldError{Field: f, Message: problem})
			continue
		}
		key := fold(tag)
		if j, dup := first[key]; dup {
			errs = append(errs, input.FieldError{Field: f, Message: fmt.Sprintf("is the tag of %s[%d] too, but for "+
				"its case, which tags are compared without", at, j)})
			continue
		}
		first[key] = i
	}
	return errs
}

// Tag is a tag of a project's nodes, as the API suggests it.
type Tag struct {
	Tag   string `json:"tag" doc:"The tag, as the most of the nodes that have it write it, or, of the spellings that as many write, the first in the order of their bytes"`
	Nodes int64  `json:"nodes" doc:"How many of the project's nodes that are not in its trash have the tag, whatever its case"`
}

// spelling is a tag as some nodes write it.
type spelling struct {
	tag  string
	uses int64
}

// suggest returns at most limit of the tags of the project that start with
// prefix, compared without regard to case, as fold compares them, each once,
// as the most of the nodes that have it write it, the most used first, then
// by their folded keys, from spellings, the tags as the nodes write them,
// each with how many nodes have it, which ProjectTags returns.
func suggest(spellings []spelling, prefix string, limit int) []Tag {
	start := fold(prefix)
	type merged struct {
		key  string
		best spelling
		uses int64
	}
	byKey := map[string]*merged{}
	var keys []string
	for _, s := range spellings {
		key := fold(s.tag)
		if !strings.HasPrefix(key, start) {
			continue
		}
		m := byKey[key]
		if m == nil {
			m = &merged{key: key, best: s}
			byKey[key] = m
			keys = append(keys, key)
		} else if s.uses > m.best.uses || s.uses == m.best.uses && s.tag < m.best.tag {
			m.best = s
		}
		m.uses += s.uses
	}
	slices.SortFunc(keys, func(a, b string) int {
		if byKey[a].uses != byKey[b].uses {
			if byKey[a].uses > byKey[b].uses {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	// A limit below 1 suggests none.
	n := min(max(limit, 0), len(keys))
	out := make([]Tag, 0, n)
	for _, k := range keys[:n] {
		out = append(out, Tag{Tag: byKey[k].best.tag, Nodes: byKey[k].uses})
	}
	return out
}

// CheckTagQuery says what is wrong with prefix as the start of a tag that
// the tags are suggested for, or returns "": at most MaxTagQueryLength
// characters, without control characters.
func CheckTagQuery(prefix string) string {
	switch {
	case utf8.RuneCountInString(prefix) > MaxTagQueryLength:
		return fmt.Sprintf("is longer than %d characters", MaxTagQueryLength)
	case !utf8.ValidString(prefix) || strings.IndexFunc(prefix, input.IsControl) >= 0:
		return "contains a control character"
	}
	return ""
}
