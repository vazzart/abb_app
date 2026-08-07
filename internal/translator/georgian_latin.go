package translator

import (
	"regexp"
	"strings"
	"unicode"
)

// georgianLatinWords are common Georgian words/stems that show up in
// latin-transliterated Georgian SMS (mostly carrier/service notifications,
// since GSM 7-bit encoding doesn't fit the Georgian alphabet). They rarely
// occur as whole words in other latin-alphabet languages.
var georgianLatinWords = map[string]bool{
	"tu": true, "rom": true, "da": true, "ara": true, "aris": true,
	"unda": true, "iqos": true, "iyos": true, "iqonie": true,
	"gamarjoba": true, "madloba": true, "gmadlobt": true,
	"shen": true, "chemi": true, "sheni": true, "chven": true, "tqven": true,
	"nomeri": true, "nomris": true, "balansi": true, "balansze": true,
	"abonenti": true, "momsaxureba": true, "gadaixade": true, "gadaxda": true,
	"gantsxadeba": true, "sheatyobinebt": true, "gaqvt": true, "gakvt": true,
	"gtxovt": true, "gaqvs": true, "sheidzleba": true, "vadamde": true,
	"gaigzavna": true, "gaigzavneba": true, "gagrzeldeba": true,
	"shenarchuneba": true, "gamortve": true, "chartva": true, "xval": true,
	"gsurs": true, "gindat": true, "rogor": true, "xar": true, "var": true,
	"kargad": true, "magram": true, "vano": true, "ki": true, "diakh": true,
}

// georgianSuffixes are common Georgian derivational/verbal suffixes.
var georgianSuffixes = []string{"deba", "deboda", "uleba", "ianoba", "oba", "uri", "uli", "eba"}

// georgianPostpositions attach directly to the noun with no space
// (e.g. balans+ze = "on the balance"), unlike most latin-alphabet languages.
var georgianPostpositions = []string{"tvis", "shi", "tan", "ze"}

// georgianBigrams are letter clusters that occur far more often in
// transliterated Georgian than in other latin-alphabet languages.
var georgianBigrams = []string{"kh", "gh", "ts", "dz", "pkh", "tkh", "chkh", "gv", "qv"}

var wordRe = regexp.MustCompile(`[A-Za-z]+`)

// LooksLikeGeorgianLatin scores text against patterns typical of Georgian
// written with Latin letters. It's a heuristic tuned against real
// carrier/service SMS text, not a real language model - it exists because
// Yandex Translate's own language auto-detection reliably misidentifies
// this text as Uzbek/Turkish/etc, even with language hints.
func LooksLikeGeorgianLatin(text string) bool {
	words := wordRe.FindAllString(text, -1)
	if len(words) == 0 {
		return false
	}

	latinLetters, totalLetters := 0, 0
	for _, r := range text {
		if !unicode.IsLetter(r) {
			continue
		}
		totalLetters++
		if r <= unicode.MaxASCII {
			latinLetters++
		}
	}
	if totalLetters == 0 || float64(latinLetters)/float64(totalLetters) < 0.9 {
		// Not (almost) pure Latin script - native Georgian/Cyrillic text is
		// detected correctly by Yandex already, so let auto-detect handle it.
		return false
	}

	score := 0.0
	for _, w := range words {
		lw := strings.ToLower(w)
		if georgianLatinWords[lw] {
			score += 2
			continue
		}
		for _, suf := range georgianSuffixes {
			if len(lw) > len(suf)+2 && strings.HasSuffix(lw, suf) {
				score++
				break
			}
		}
		for _, pp := range georgianPostpositions {
			if len(lw) > len(pp)+3 && strings.HasSuffix(lw, pp) {
				score += 0.5
				break
			}
		}
		for _, bg := range georgianBigrams {
			if strings.Contains(lw, bg) {
				score += 0.5
				break
			}
		}
		if maxConsonantRun(lw) >= 3 {
			score += 0.5
		}
	}

	return score >= 3
}

func maxConsonantRun(word string) int {
	max, cur := 0, 0
	for _, r := range word {
		if strings.ContainsRune("aeiou", r) {
			cur = 0
			continue
		}
		cur++
		if cur > max {
			max = cur
		}
	}
	return max
}
