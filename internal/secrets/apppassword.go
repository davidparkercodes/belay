package secrets

import "bytes"

var appContextWords = [][]byte{
	[]byte("password"), []byte("passwd"), []byte("passcode"), []byte("pass:"), []byte("pass "),
	[]byte("pass="), []byte("pwd"), []byte("credential"), []byte("secret"), []byte("token"),
	[]byte("login"), []byte("log in"), []byte("sign in"), []byte("signin"), []byte("auth"),
}

var appContiguousReject = [][]byte{
	[]byte("pass"), []byte("word"), []byte("secret"), []byte("token"), []byte("auth"), []byte("login"),
}

func (sc *scan) scanAppPasswords(all []anchor) {
	var anchors []int
	for _, a := range all {
		if a.word < 0 || anchorWords[a.word].app {
			anchors = append(anchors, a.pos)
		}
	}
	if len(anchors) == 0 {
		return
	}
	const before, after = 120, 220
	lo, hi := -1, -1
	for _, a := range anchors {
		s, e := max(a-before, 0), min(a+after, len(sc.data))
		if lo >= 0 && s <= hi {
			hi = max(hi, e)
			continue
		}
		if lo >= 0 {
			sc.appWindow(lo, hi)
		}
		lo, hi = s, e
	}
	sc.appWindow(lo, hi)
}

func isLowerLetter(c byte) bool { return c >= 'a' && c <= 'z' }

func letterBoundaryBefore(d []byte, i int) bool {
	if i == 0 || !isAlpha(d[i-1]) {
		return true
	}
	return (d[i-1] == 'n' || d[i-1] == 't' || d[i-1] == 'r') && i >= 2 && d[i-2] == '\\'
}

func lowerRun4(d []byte, i int) bool {
	if i+4 > len(d) {
		return false
	}
	for k := i; k < i+4; k++ {
		if !isLowerLetter(d[k]) {
			return false
		}
	}
	return true
}

func (sc *scan) appWindow(lo, hi int) {
	d := sc.data
	i := lo
	for i < hi {
		if i > 0 && d[i-1] == '\\' && (d[i] == 'n' || d[i] == 't' || d[i] == 'r') {
			i++
			continue
		}
		if !isLowerLetter(d[i]) || !letterBoundaryBefore(d, i) || sc.inDense(i) {
			i++
			continue
		}
		if end, ok := appPasswordAt(d, i); ok && sc.appContext(i, end) && plausibleAppPassword(d, i, end) {
			sc.add(RuleGoogleAppPassword, i, end)
			i = end
			continue
		}
		for i < hi && isAlpha(d[i]) {
			i++
		}
	}
}

func appPasswordAt(d []byte, i int) (int, bool) {
	if !lowerRun4(d, i) {
		return 0, false
	}
	j := i + 4
	spaced := j < len(d) && d[j] == ' '
	for g := 1; g < 4; g++ {
		if spaced {
			if j >= len(d) || d[j] != ' ' {
				return 0, false
			}
			j++
		}
		if !lowerRun4(d, j) {
			return 0, false
		}
		j += 4
	}
	if j < len(d) && isAlpha(d[j]) {
		return 0, false
	}
	return j, true
}

func plausibleAppPassword(d []byte, s, e int) bool {
	v := d[s:e]
	spaced := e-s == 19
	var groups [4][]byte
	for g := 0; g < 4; g++ {
		if spaced {
			groups[g] = v[g*5 : g*5+4]
		} else {
			groups[g] = v[g*4 : g*4+4]
		}
	}
	if distinct(v) < 8 {
		return false
	}
	common := 0
	for _, g := range groups {
		if commonWord(g) {
			common++
		}
	}
	pre := toLower(d[max(s-24, 0):s])
	for _, w := range []string{"placeholder", "for example", "e.g.", "eg:", "format"} {
		if bytes.Contains(pre, []byte(w)) {
			return false
		}
	}
	if spaced {
		if common >= 2 {
			return false
		}
		if s >= 5 && d[s-1] == ' ' && lowerRun4(d, s-5) && letterBoundaryBefore(d, s-5) {
			return false
		}
		if e+5 <= len(d) && d[e] == ' ' && lowerRun4(d, e+1) && (e+5 == len(d) || !isAlpha(d[e+5])) {
			return false
		}
		return true
	}
	if common >= 1 || vowels(v) > 5 || !contiguousContext(d, s) {
		return false
	}
	if s > 0 && bytes.IndexByte([]byte("/.-_@"), d[s-1]) >= 0 {
		return false
	}
	if e < len(d) && bytes.IndexByte([]byte("/.-_@"), d[e]) >= 0 && (e+1 < len(d) && isAlnum(d[e+1])) {
		return false
	}
	for _, w := range appContiguousReject {
		if bytes.Contains(v, w) {
			return false
		}
	}
	return true
}

func (sc *scan) appContext(s, e int) bool {
	lo, hi := max(s-160, 0), min(e+80, len(sc.data))
	w := toLower(sc.data[lo:hi])
	for _, k := range appContextWords {
		if bytes.Contains(w, k) {
			return true
		}
	}
	return hasEmail(w)
}

func hasEmail(w []byte) bool {
	off := 0
	for {
		i := bytes.IndexByte(w[off:], '@')
		if i < 0 {
			return false
		}
		i += off
		off = i + 1
		if i == 0 || !(isAlnum(w[i-1]) || w[i-1] == '.' || w[i-1] == '_' || w[i-1] == '+' || w[i-1] == '-') {
			continue
		}
		j := i + 1
		dot := false
		for j < len(w) && (isAlnum(w[j]) || w[j] == '-' || w[j] == '.') {
			if w[j] == '.' && j+1 < len(w) && isAlpha(w[j+1]) {
				dot = true
			}
			j++
		}
		if dot && j > i+3 {
			return true
		}
	}
}

func vowels(b []byte) int {
	n := 0
	for _, c := range b {
		switch c {
		case 'a', 'e', 'i', 'o', 'u':
			n++
		}
	}
	return n
}

func contiguousContext(d []byte, s int) bool {
	p := s
	for p > 0 && (d[p-1] == ' ' || d[p-1] == '\t' || d[p-1] == '"' || d[p-1] == '\'') {
		p--
	}
	if p == 0 || (d[p-1] != '=' && d[p-1] != ':') {
		return false
	}
	lo := p - 1
	for lo > 0 && p-lo < 160 && d[lo-1] != '\n' {
		lo--
	}
	lw := toLower(d[lo:p])
	for _, k := range []string{"password", "passwd", "pwd", "app pass", "app_pass", "passcode"} {
		if bytes.Contains(lw, []byte(k)) {
			return true
		}
	}
	return false
}
