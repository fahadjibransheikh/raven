package message

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	signInCodeTextLimit   = 2000
	signInCodeAfterWindow = 60 // candidate may follow a keyword by this many bytes
	signInCodeBeforeLimit = 30 // or precede one by this many ("123456 is your code")
	signInCodeExclWindow  = 25
)

var (
	signInKeywordRe   = regexp.MustCompile(`(?i)\b(?:code|verification|verify|otp|one[- ]time|passcode|pin|sign[- ]?in|log[- ]?in)\b`)
	signInExclusionRe = regexp.MustCompile(`(?i)\b(?:order|invoice|tracking|ref|reference|receipt|zip|postal|area|country|promo|discount|coupon|gift|booking|reservation|error|status|ticket|tel|fax)\b`)
	signInRunRe       = regexp.MustCompile(`[A-Za-z0-9]+`)
	signInStateZipRe  = regexp.MustCompile(`,\s*[A-Z]{2}\s+$`)
)

type signInCandidate struct {
	value      string
	start, end int
}

// ExtractSignInCode returns the sign-in / verification code in a message, or ""
// when there is none or the match is doubtful. It prefers the subject, then the
// first part of the text body. Callers must not log the result.
func ExtractSignInCode(subject, text string) string {
	if code := findSignInCode(subject); code != "" {
		return code
	}
	if len(text) > signInCodeTextLimit {
		text = strings.ToValidUTF8(text[:signInCodeTextLimit], "")
	}
	return findSignInCode(text)
}

func findSignInCode(s string) string {
	keywords := signInKeywordRe.FindAllStringIndex(s, -1)
	if len(keywords) == 0 {
		return ""
	}
	runs := signInRunRe.FindAllStringIndex(s, -1)
	for i, run := range runs {
		var cands []signInCandidate
		if i+1 < len(runs) {
			if c, ok := signInPair(s, run, runs[i+1]); ok {
				cands = append(cands, c)
			}
		}
		if w := s[run[0]:run[1]]; signInToken(w) {
			cands = append(cands, signInCandidate{w, run[0], run[1]})
		}
		for _, c := range cands {
			if signInCandidateOK(s, c, keywords) {
				return c.value
			}
		}
	}
	return ""
}

// signInPair joins two 3-4 digit groups split by one hyphen or space ("123-456").
func signInPair(s string, a, b []int) (signInCandidate, bool) {
	if b[0] != a[1]+1 || (s[a[1]] != '-' && s[a[1]] != ' ') {
		return signInCandidate{}, false
	}
	x, y := s[a[0]:a[1]], s[b[0]:b[1]]
	if len(x) < 3 || len(x) > 4 || len(y) < 3 || len(y) > 4 || !allDigits(x) || !allDigits(y) {
		return signInCandidate{}, false
	}
	return signInCandidate{x + y, a[0], b[1]}, true
}

// signInToken accepts 4-8 digits, or 4-8 uppercase alphanumerics with a digit.
func signInToken(w string) bool {
	if len(w) < 4 || len(w) > 8 {
		return false
	}
	hasDigit := false
	for i := 0; i < len(w); i++ {
		c := w[i]
		switch {
		case c >= '0' && c <= '9':
			hasDigit = true
		case c >= 'A' && c <= 'Z':
		default:
			return false
		}
	}
	return hasDigit
}

func allDigits(w string) bool {
	for i := 0; i < len(w); i++ {
		if w[i] < '0' || w[i] > '9' {
			return false
		}
	}
	return w != ""
}

func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

func signInCandidateOK(s string, c signInCandidate, keywords [][]int) bool {
	if !signInNearKeyword(c, keywords) || signInBadContext(s, c) {
		return false
	}
	// A year (1999, 2026) is never a code.
	if len(c.value) == 4 && allDigits(c.value) && (c.value[:2] == "19" || c.value[:2] == "20") {
		return false
	}
	// "Seattle, WA 98101"
	if len(c.value) == 5 && allDigits(c.value) {
		tail := s[:c.start]
		if len(tail) > 10 {
			tail = tail[len(tail)-10:]
		}
		if signInStateZipRe.MatchString(tail) {
			return false
		}
	}
	// "order 12345 ... code": an exclusion word right before the candidate wins
	// unless a stronger keyword than the bare word "code" sits between them.
	pre := s[max(0, c.start-signInCodeExclWindow):c.start]
	if locs := signInExclusionRe.FindAllStringIndex(pre, -1); len(locs) > 0 {
		rest := pre[locs[len(locs)-1][1]:]
		strong := false
		for _, m := range signInKeywordRe.FindAllString(rest, -1) {
			if !strings.EqualFold(m, "code") {
				strong = true
			}
		}
		if !strong {
			return false
		}
	}
	return true
}

func signInNearKeyword(c signInCandidate, keywords [][]int) bool {
	for _, k := range keywords {
		if d := c.start - k[1]; d >= 0 && d <= signInCodeAfterWindow {
			return true
		}
		if d := k[0] - c.end; d >= 0 && d <= signInCodeBeforeLimit {
			return true
		}
	}
	return false
}

// signInBadContext rejects candidates that sit inside prices, dates, times,
// phone numbers, ids and similar punctuation-joined numbers.
func signInBadContext(s string, c signInCandidate) bool {
	if c.start > 0 {
		p, size := utf8.DecodeLastRuneInString(s[:c.start])
		prev := s[:c.start-size]
		switch {
		case p == '-':
			// "G-123456" (Google) is a code; "555-1234" is not.
			if !(len(prev) >= 1 && prev[len(prev)-1] >= 'A' && prev[len(prev)-1] <= 'Z' &&
				(len(prev) == 1 || !isAlnumByte(prev[len(prev)-2]))) {
				return true
			}
		case strings.ContainsRune("+/$€£#%@", p):
			return true
		case p == '.' || p == ',' || p == ':' || p == ' ':
			if r, _ := utf8.DecodeLastRuneInString(prev); isASCIIDigit(r) {
				return true
			}
		}
	}
	if c.end < len(s) {
		n, size := utf8.DecodeRuneInString(s[c.end:])
		next := s[c.end+size:]
		switch {
		case strings.ContainsRune("-+/%", n):
			return true
		case n == '.' || n == ',' || n == ':' || n == ' ':
			if r, _ := utf8.DecodeRuneInString(next); isASCIIDigit(r) {
				return true
			}
		}
	}
	return false
}

func isAlnumByte(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}
