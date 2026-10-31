package secrets

import (
	"bytes"
	"math"
)

type valueKind int

const (
	kindQuoted valueKind = iota
	kindUnquoted
	kindProse
)

var placeholderParts = [][]byte{
	[]byte("your"), []byte("example"), []byte("changeme"), []byte("change_me"), []byte("change-me"),
	[]byte("placeholder"), []byte("xxxx"), []byte("****"), []byte("...."), []byte("redacted"),
	[]byte("dummy"), []byte("sample"), []byte("insert"), []byte("replace"), []byte("todo"),
	[]byte("fixme"), []byte("<"), []byte(">"), []byte("${"), []byte("{{"), []byte("}}"),
	[]byte("$("), []byte("%s"), []byte("%("), []byte("%v"), []byte("0000000"), []byte("1234567"),
	[]byte("abcdefg"), []byte("abc123"), []byte("fake"), []byte("not-a-real"), []byte("notreal"),
	[]byte("%d"), []byte("%r"), []byte("=%"), []byte("test-"), []byte("test_"), []byte("-test"),
	[]byte("_test"), []byte("mock"),
}

var exactPlaceholders = map[string]bool{
	"password": true, "passwd": true, "secret": true, "token": true, "changeme": true,
	"admin": true, "root": true, "test": true, "testing": true, "none": true, "null": true,
	"nil": true, "undefined": true, "true": true, "false": true, "string": true, "required": true,
	"default": true, "empty": true, "hidden": true, "masked": true, "optional": true, "foobar": true,
	"guest": true, "user": true, "username": true, "pass": true, "secret123": true, "password1": true,
	"password123": true, "letmein": true, "qwerty": true, "postgres": true, "mysql": true,
}

var refPrefixes = [][]byte{
	[]byte("$"), []byte("%"), []byte("env."), []byte("env:"), []byte("env["), []byte("process.env"),
	[]byte("os.getenv"), []byte("os.environ"), []byte("getenv"), []byte("system.getenv"),
	[]byte("secrets."), []byte("vars."), []byte("vault:"), []byte("config."), []byte("cfg."),
	[]byte("settings."), []byte("self."), []byte("this."), []byte("import.meta"), []byte("!"),
	[]byte("*"), []byte("&"), []byte("file:"), []byte("http://"), []byte("https://"), []byte("/"),
	[]byte("./"), []byte("../"), []byte("~/"), []byte("arn:"), []byte("op://"),
}

var typeWords = map[string]bool{
	"string": true, "str": true, "number": true, "int": true, "bool": true, "boolean": true,
	"any": true, "bytes": true, "[]byte": true, "text": true, "varchar": true, "secretstr": true,
	"uuid": true, "object": true, "unknown": true, "char": true,
}

func hasPlaceholder(lv []byte) bool {
	for _, p := range placeholderParts {
		if bytes.Contains(lv, p) {
			return true
		}
	}
	return false
}

func classes(b []byte) (upper, lower, digit, symbol bool) {
	for _, c := range b {
		switch {
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= 'a' && c <= 'z':
			lower = true
		case isDigit(c):
			digit = true
		case c == '_' || c == '-' || c == '.':
		default:
			symbol = true
		}
	}
	return
}

func distinct(b []byte) int {
	var seen [256]bool
	n := 0
	for _, c := range b {
		if !seen[c] {
			seen[c] = true
			n++
		}
	}
	return n
}

func shannon(b []byte) float64 {
	if len(b) == 0 {
		return 0
	}
	var counts [256]int
	for _, c := range b {
		counts[c]++
	}
	h := 0.0
	n := float64(len(b))
	for _, k := range counts {
		if k > 0 {
			p := float64(k) / n
			h -= p * math.Log2(p)
		}
	}
	return h
}

func lowerRuns(b []byte, minRun int) int {
	runs, cur := 0, 0
	for _, c := range b {
		if c >= 'a' && c <= 'z' {
			cur++
			if cur == minRun {
				runs++
			}
		} else {
			cur = 0
		}
	}
	return runs
}

func looksRandom(b []byte) bool {
	if len(b) < 16 || distinct(b) < 10 || shannon(b) < 3.5 {
		return false
	}
	u, l, d, _ := classes(b)
	n := 0
	for _, x := range []bool{u, l, d} {
		if x {
			n++
		}
	}
	return n >= 2 && lowerRuns(b, 4) <= 1 && !identLike(b)
}

func allDigits(b []byte) bool {
	for _, c := range b {
		if !isDigit(c) && c != '.' && c != ',' && c != '_' && c != ':' {
			return false
		}
	}
	return true
}

func versionLike(b []byte) bool {
	if len(b) > 0 && (b[0] == 'v' || b[0] == 'V') {
		b = b[1:]
	}
	if len(b) == 0 || !isDigit(b[0]) {
		return false
	}
	for _, c := range b {
		if !isDigit(c) && c != '.' && c != '-' {
			return false
		}
	}
	return bytes.IndexByte(b, '.') >= 0
}

func looksEmail(b []byte) bool {
	at := bytes.IndexByte(b, '@')
	return at > 0 && bytes.IndexByte(b[at:], '.') > 0
}

func isSnakeIdentWithKeyword(lv []byte) bool {
	if len(lv) == 0 || !isAlpha(lv[0]) || bytes.IndexByte(lv, '_') < 0 {
		return false
	}
	for _, c := range lv {
		if !isWord(c) {
			return false
		}
	}
	for _, k := range []string{"pass", "secret", "token", "key", "auth", "pwd", "cred"} {
		if bytes.Contains(lv, []byte(k)) {
			return true
		}
	}
	return false
}

func hasPasswordSymbol(b []byte) bool {
	return bytes.ContainsAny(b, "!@#$%^&*~?")
}

func plausibleSecret(v []byte, kind valueKind, minLen int) bool {
	if len(v) < minLen || len(v) > 1024 {
		return false
	}
	if bytes.ContainsAny(v, " \t\r\n") {
		return false
	}
	if distinct(v) < 4 {
		return false
	}
	for _, c := range v {
		if c >= 0x80 || c < 0x20 {
			return false
		}
	}
	if kebabWords(v) {
		return false
	}
	lv := toLower(v)
	if exactPlaceholders[string(lv)] || hasPlaceholder(lv) {
		return false
	}
	for _, p := range refPrefixes {
		if bytes.HasPrefix(lv, p) {
			return false
		}
	}
	if last := v[len(v)-1]; last == '-' || last == '_' || last == ':' {
		return false
	}
	if fileLineRef(lv) {
		return false
	}
	if bytes.HasPrefix(lv, []byte("0x")) && allHex(lv[2:]) {
		return false
	}
	if allDigits(v) || versionLike(v) || looksEmail(v) || isSnakeIdentWithKeyword(lv) {
		return false
	}
	if bytes.Contains(lv, []byte("://")) {
		return false
	}
	for _, w := range credentialWordsInValue {
		if bytes.Contains(lv, w) {
			return false
		}
	}
	_, _, d, sym := classes(v)
	if kind == kindQuoted {
		if !isAlnum(v[0]) && bytes.IndexByte([]byte("!@#$%^&*~+=_-"), v[0]) < 0 || bytes.Contains(v, []byte(`\\`)) || bytes.Contains(v, []byte(`\x`)) || bytes.Contains(v, []byte(`\u`)) {
			return false
		}
		return d || sym || looksRandom(v)
	}
	if typeWords[string(lv)] || bytes.ContainsAny(v, "()[]{}<>|,") || dottedIdent(v) {
		return false
	}
	for _, op := range codeOperators {
		if bytes.Contains(v, op) {
			return false
		}
	}
	if q := bytes.IndexByte(v, '?'); q >= 0 && bytes.IndexByte(v[q:], ':') > 0 {
		return false
	}
	if !isAlnum(v[0]) || bytes.Contains(v, []byte("?.")) || (v[len(v)-1] == '!' && bytes.IndexByte(v, '.') >= 0) {
		return false
	}
	if kind == kindProse && commonWord(lv) {
		return false
	}
	return d || hasPasswordSymbol(v)
}

var credentialWordsInValue = [][]byte{
	[]byte("token"), []byte("secret"), []byte("password"), []byte("passwd"),
}

func dottedIdent(v []byte) bool {
	if bytes.IndexByte(v, '.') < 0 {
		return false
	}
	for _, part := range bytes.Split(v, []byte{'.'}) {
		if len(part) == 0 || !(isAlpha(part[0]) || part[0] == '_') {
			return false
		}
		for _, c := range part {
			if !isWord(c) {
				return false
			}
		}
	}
	return true
}

func identLike(b []byte) bool {
	camel, run := 0, 0
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			run++
			if run >= 7 {
				return true
			}
			if run == 4 && i >= 4 && b[i-4] >= 'A' && b[i-4] <= 'Z' {
				camel++
			}
		} else {
			run = 0
		}
	}
	return camel >= 2
}

var codeOperators = [][]byte{
	[]byte("=="), []byte("!="), []byte("=>"), []byte("&&"), []byte("||"), []byte("->"),
}

func allHex(b []byte) bool {
	for _, c := range b {
		if !isHex(c) {
			return false
		}
	}
	return true
}

func kebabWords(v []byte) bool {
	if bytes.IndexAny(v, "-_") < 0 {
		return false
	}
	digits := 0
	for _, c := range v {
		switch {
		case c >= 'a' && c <= 'z', c == '-', c == '_':
		case isDigit(c):
			digits++
		default:
			return false
		}
	}
	return digits <= 2 && lowerRuns(v, 5) > 0
}

func fileLineRef(lv []byte) bool {
	c := bytes.LastIndexByte(lv, ':')
	if c < 0 || c == len(lv)-1 {
		return false
	}
	for _, x := range lv[c+1:] {
		if !isDigit(x) {
			return false
		}
	}
	dot := bytes.LastIndexByte(lv[:c], '.')
	if dot < 0 || c-dot-1 < 1 || c-dot-1 > 5 {
		return false
	}
	for _, x := range lv[dot+1 : c] {
		if !isAlpha(x) {
			return false
		}
	}
	return true
}
