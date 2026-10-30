package processing

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/avi-p/yesbook/internal/document"
)

func isHebrew(r rune) bool { return r >= 0x05D0 && r <= 0x05EA }

func isFinalForm(r rune) bool {
	switch r {
	case 'ך', 'ם', 'ן', 'ף', 'ץ':
		return true
	default:
		return false
	}
}

// countLetters returns the number of Hebrew and Latin letters in s.
func countLetters(s string) (hebrew, latin int) {
	for _, r := range s {
		switch {
		case isHebrew(r):
			hebrew++
		case r < 0x250 && unicode.IsLetter(r):
			latin++
		}
	}
	return hebrew, latin
}

// detectLanguage returns "he", "en" or "" when s has no letters.
func detectLanguage(s string) string {
	hebrew, latin := countLetters(s)
	switch {
	case hebrew > 0 && hebrew >= latin:
		return "he"
	case latin > 0:
		return "en"
	default:
		return ""
	}
}

func detectDirection(s string) document.Direction {
	hebrew, latin := countLetters(s)
	if hebrew > latin {
		return document.DirectionRTL
	}
	return document.DirectionLTR
}

// hebrewOrderScore measures whether Hebrew text is in logical order. Final
// letter forms (ך ם ן ף ץ) belong at the end of a word, so text extracted in
// visual (reversed) order shows them at the start. The score is the share of
// final forms found at word ends; ok is false when there are too few to judge.
func hebrewOrderScore(s string) (score float64, ok bool) {
	var atEnd, atStart int
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) }) {
		first, _ := utf8.DecodeRuneInString(w)
		last, _ := utf8.DecodeLastRuneInString(w)
		if utf8.RuneCountInString(w) < 2 || !isHebrew(first) && !isHebrew(last) {
			continue
		}
		if isFinalForm(last) {
			atEnd++
		}
		if isFinalForm(first) {
			atStart++
		}
	}
	if atEnd+atStart < 3 {
		return 1, false
	}
	return float64(atEnd) / float64(atEnd+atStart), true
}

// words returns the normalized word set of s, reversing Hebrew words when
// s is in visual order so both orders compare equal.
func words(s string) map[string]struct{} {
	reversed := false
	if score, ok := hebrewOrderScore(s); ok && score < 0.5 {
		reversed = true
	}
	set := make(map[string]struct{})
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if reversed {
			r := []rune(w)
			slices.Reverse(r)
			w = string(r)
		}
		set[w] = struct{}{}
	}
	return set
}

// agreement is the Dice coefficient of the word sets of a and b.
func agreement(a, b string) float64 {
	wa, wb := words(a), words(b)
	if len(wa)+len(wb) == 0 {
		return 1
	}
	common := 0
	for w := range wa {
		if _, ok := wb[w]; ok {
			common++
		}
	}
	return 2 * float64(common) / float64(len(wa)+len(wb))
}

// countChars counts non-space runes.
func countChars(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

// legibility is the share of non-space runes that are not replacement or
// private-use characters, which broken font encodings produce.
func legibility(s string) float64 {
	total, bad := 0, 0
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		total++
		if r == utf8.RuneError || unicode.Is(unicode.Co, r) || unicode.IsControl(r) {
			bad++
		}
	}
	if total == 0 {
		return 1
	}
	return 1 - float64(bad)/float64(total)
}
