package secrets

import "bytes"

type anchorWord struct {
	word []byte
	cred bool
	app  bool
}

var anchorWords = []anchorWord{
	{[]byte("pass"), true, true}, {[]byte("pwd"), true, true}, {[]byte("secret"), true, true},
	{[]byte("token"), true, true}, {[]byte("key"), true, false}, {[]byte("auth"), true, true},
	{[]byte("credential"), true, true}, {[]byte("bearer"), true, false},
	{[]byte("login"), false, true}, {[]byte("signin"), false, true},
}

var anchorsByFirst = func() [256][]int {
	var t [256][]int
	for i, a := range anchorWords {
		t[a.word[0]] = append(t[a.word[0]], i)
	}
	return t
}()

type anchor struct {
	pos  int
	word int
}

func hasPrefixFold(d []byte, i int, w []byte) bool {
	if i+len(w) > len(d) {
		return false
	}
	for k, c := range w {
		x := d[i+k]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if x != c {
			return false
		}
	}
	return true
}

var anchorFirst = func() [256]bool {
	var t [256]bool
	for _, a := range anchorWords {
		t[a.word[0]] = true
		t[a.word[0]-('a'-'A')] = true
	}
	t['@'] = true
	return t
}()

func (sc *scan) findAnchors() []anchor {
	d := sc.data
	var out []anchor
	pos := 0
	for di := 0; di <= len(sc.dense); di++ {
		end := len(d)
		if di < len(sc.dense) {
			end = sc.dense[di].start
		}
		out = findAnchorsIn(d, pos, end, out)
		if di < len(sc.dense) {
			pos = sc.dense[di].end
		}
	}
	return out
}

func findAnchorsIn(d []byte, i, end int, out []anchor) []anchor {
	for ; i < end; i++ {
		c := d[i]
		if !anchorFirst[c] {
			continue
		}
		if c == '@' {
			out = append(out, anchor{i, -1})
			continue
		}
		if c <= 'Z' {
			c += 'a' - 'A'
		}
		for _, w := range anchorsByFirst[c] {
			if hasPrefixFold(d, i, anchorWords[w].word) {
				out = append(out, anchor{i, w})
				break
			}
		}
	}
	return out
}

func (sc *scan) scanKeywords(anchors []anchor) {
	lastEnd := -1
	for _, a := range anchors {
		if a.word < 0 || !anchorWords[a.word].cred || a.pos < lastEnd {
			continue
		}
		is, ie := identBounds(sc.data, a.pos, len(anchorWords[a.word].word))
		lastEnd = ie
		sc.keywordAt(is, ie)
	}
}

var credSegments = map[string]bool{
	"password": true, "passwd": true, "passphrase": true, "passcode": true, "pass": true, "pwd": true,
	"secret": true, "token": true, "apikey": true, "secretkey": true, "accesskey": true,
	"privatekey": true, "authtoken": true, "accesstoken": true, "refreshtoken": true,
	"clientsecret": true, "credential": true, "credentials": true, "authorization": true,
	"bearer": true, "authkey": true, "signingkey": true, "masterkey": true, "encryptionkey": true,
	"auth": true, "apitoken": true,
}

var passwordSegments = map[string]bool{
	"password": true, "passwd": true, "passphrase": true, "passcode": true, "pass": true, "pwd": true,
}

var keyQualifiers = map[string]bool{
	"api": true, "access": true, "private": true, "secret": true, "auth": true,
	"signing": true, "encryption": true, "master": true,
}

var excludeSegments = map[string]bool{
	"tokens": true, "tokenizer": true, "tokenize": true, "tokenized": true, "count": true,
	"length": true, "len": true, "max": true, "min": true, "limit": true, "type": true, "types": true,
	"url": true, "uri": true, "endpoint": true, "path": true, "file": true, "filename": true,
	"dir": true, "name": true, "header": true, "field": true, "label": true, "prompt": true,
	"policy": true, "expiry": true, "expires": true, "expiration": true, "exp": true, "ttl": true,
	"timeout": true, "env": true, "var": true, "regex": true, "pattern": true, "hint": true,
	"size": true, "kind": true, "usage": true, "budget": true, "index": true, "idx": true,
	"mode": true, "format": true, "validator": true, "validation": true, "strength": true,
	"rotation": true, "manager": true, "store": true, "provider": true, "required": true,
	"enabled": true, "example": true, "placeholder": true, "hash": true, "hashed": true,
	"digest": true, "prefix": true, "suffix": true, "id": true, "ids": true, "cache": true,
	"ref": true, "location": true, "scope": true, "scopes": true, "version": true, "public": true,
	"pub": true, "fingerprint": true, "input": true, "error": true, "err": true, "msg": true,
	"message": true, "text": true, "help": true, "title": true, "description": true, "desc": true,
	"reset": true, "forgot": true, "is": true, "has": true, "show": true, "hide": true,
	"visible": true, "toggle": true, "valid": true, "invalid": true, "verify": true, "check": true,
	"next": true, "page": true, "pagination": true, "continuation": true, "sync": true,
	"csrf": true, "xsrf": true, "source": true,
	"interval": true, "class": true, "classname": true, "icon": true, "button": true, "btn": true,
	"form": true, "dialog": true, "modal": true, "view": true, "screen": true, "component": true,
	"schema": true, "column": true, "col": true, "test": true, "tests": true, "spec": true,
	"mechanism": true, "method": true, "algorithm": true, "algo": true, "scheme": true,
	"cipher": true, "options": true, "option": true, "opts": true, "flags": true, "flag": true,
	"context": true, "ctx": true, "handler": true, "middleware": true, "service": true,
	"callback": true, "request": true, "req": true, "response": true, "resp": true,
	"h": true, "c": true, "go": true, "py": true, "js": true, "ts": true, "json": true,
	"keys": true, "date": true, "yaml": true, "yml": true, "toml": true, "md": true, "txt": true, "cfg": true, "ini": true,
}

var passQualifiers = map[string]bool{
	"login": true, "smtp": true, "imap": true, "db": true, "database": true, "wifi": true,
	"admin": true, "root": true, "user": true, "ftp": true, "ssh": true, "vpn": true, "my": true,
	"account": true, "email": true, "mail": true, "sudo": true,
}

func isIdentifier(b []byte) bool {
	if len(b) == 0 || !(isAlpha(b[0]) || b[0] == '_') {
		return false
	}
	for _, c := range b {
		if !isWord(c) {
			return false
		}
	}
	return true
}

var errorWords = map[string]bool{
	"unrecognized": true, "unexpected": true, "invalid": true, "bad": true, "missing": true,
	"unknown": true, "illegal": true, "expected": true, "malformed": true, "empty": true,
	"no": true, "unsupported": true, "expired": true,
}

func isIdentChar(c byte) bool { return isWord(c) || c == '-' || c == '.' }

func identBounds(d []byte, i, n int) (int, int) {
	is := i
	for is > 0 && i-is < 64 && isIdentChar(d[is-1]) {
		is--
	}
	if is >= 1 && is < i && d[is-1] == '\\' && (d[is] == 'n' || d[is] == 't' || d[is] == 'r') {
		is++
	}
	ie := i + n
	for ie < len(d) && ie-i < 64 && isIdentChar(d[ie]) {
		ie++
	}
	for ie > i+n && (d[ie-1] == '.' || d[ie-1] == '-') {
		ie--
	}
	for is < i && (d[is] == '.' || d[is] == '-') {
		is++
	}
	return is, ie
}

func segments(id []byte) []string {
	var out []string
	start := -1
	flush := func(end int) {
		if start >= 0 && end > start {
			out = append(out, string(toLower(id[start:end])))
		}
		start = -1
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !isAlnum(c) {
			flush(i)
			continue
		}
		if start < 0 {
			start = i
			continue
		}
		prev := id[i-1]
		isUp := c >= 'A' && c <= 'Z'
		if isUp && prev >= 'a' && prev <= 'z' {
			flush(i)
			start = i
		} else if isUp && prev >= 'A' && prev <= 'Z' && i+1 < len(id) && id[i+1] >= 'a' && id[i+1] <= 'z' {
			flush(i)
			start = i
		}
	}
	flush(len(id))
	return out
}

func classify(segs []string, prev string) (cred, password, aws bool) {
	if errorWords[prev] || len(segs) == 0 {
		return false, false, false
	}
	if last := segs[len(segs)-1]; last == "encryption" || last == "encoding" {
		return false, false, false
	}
	hasSecret := false
	for i, s := range segs {
		if excludeSegments[s] {
			return false, false, false
		}
		if credSegments[s] {
			cred = true
		}
		if passwordSegments[s] {
			password = true
		}
		if s == "secret" {
			hasSecret = true
		}
		if s == "aws" {
			aws = true
		}
		if s == "key" {
			q := prev
			if i > 0 {
				q = segs[i-1]
			}
			if keyQualifiers[q] {
				cred = true
			}
		}
	}
	return cred, password, aws && hasSecret
}

func prevWordBytes(d []byte, is int) []byte {
	if is < 2 || d[is-1] != ' ' {
		return nil
	}
	j := is - 1
	k := j
	for k > 0 && isAlpha(d[k-1]) && j-k < 16 {
		k--
	}
	return d[k:j]
}

type classResult struct{ cred, password, aws bool }

func (sc *scan) classifyCached(is, ie int) (bool, bool, bool) {
	d := sc.data
	prevB := prevWordBytes(d, is)
	var kb [256]byte
	key := append(kb[:0], d[is:ie]...)
	key = append(key, '|')
	for _, c := range prevB {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		key = append(key, c)
	}
	if r, ok := sc.classes[string(key)]; ok {
		return r.cred, r.password, r.aws
	}
	segs := segments(d[is:ie])
	prev := string(toLower(prevB))
	var r classResult
	r.cred, r.password, r.aws = classify(segs, prev)
	if len(segs) == 1 && segs[0] == "pass" && prev != "" && !passQualifiers[prev] {
		r.cred = false
	}
	if sc.classes == nil {
		sc.classes = map[string]classResult{}
	}
	sc.classes[string(key)] = r
	return r.cred, r.password, r.aws
}

func (sc *scan) keywordAt(is, ie int) {
	d := sc.data
	if string(d[is:ie]) == "PASS" || (ie-is >= 20 && shannon(d[is:ie]) > 4) {
		return
	}
	if is > 0 && d[is-1] == '/' {
		return
	}
	cred, pw, aws := sc.classifyCached(is, ie)
	if !cred {
		return
	}
	minLen := 8
	if pw {
		minLen = 6
	}
	p := ie
	if p < len(d) && is > 0 && (d[p] == '"' || d[p] == '\'') && d[is-1] == d[p] {
		p++
	} else if p+1 < len(d) && d[p] == '\\' && d[p+1] == '"' {
		p += 2
	}
	p = skipBlank(d, p)
	if n := separatorLen(d, p, is); n > 0 {
		vs, ve, kind, ok := parseValue(d, skipBlank(d, p+n))
		if ok && kind == kindUnquoted && typeWords[string(toLower(d[vs:ve]))] {
			q := skipBlank(d, ve)
			if q < len(d) && d[q] == '=' && (q+1 >= len(d) || d[q+1] != '=') {
				vs, ve, kind, ok = parseValue(d, skipBlank(d, q+1))
			}
		}
		if ok && kind == kindUnquoted && is > 0 && (d[is-1] == '?' || d[is-1] == '&') {
			if k := bytes.IndexByte(d[vs:ve], '&'); k >= 0 {
				ve = vs + k
			}
		}
		if ok && kind == kindUnquoted && isIdentifier(d[vs:ve]) {
			if q := skipBlank(d, ve); q < len(d) && bytes.IndexByte([]byte(",)]|=;"), d[q]) >= 0 {
				ok = false
			}
		}
		if ok && plausibleSecret(d[vs:ve], kind, minLen) {
			rule := RuleAssignment
			if aws && ve-vs == 40 {
				rule = RuleAWSSecretKey
			}
			sc.add(rule, vs, ve)
			return
		}
	} else if sc.proseAt(ie, minLen) {
		return
	}
	sc.entropyAt(ie)
}

func skipBlank(d []byte, p int) int {
	for p < len(d) && (d[p] == ' ' || d[p] == '\t') {
		p++
	}
	return p
}

func separatorLen(d []byte, p, is int) int {
	if p >= len(d) {
		return 0
	}
	next := byte(0)
	if p+1 < len(d) {
		next = d[p+1]
	}
	switch d[p] {
	case '=':
		switch next {
		case '=':
			return 0
		case '>':
			return 2
		}
		return 1
	case ':':
		switch next {
		case '=':
			return 2
		case ':':
			return 0
		}
		return 1
	case '>':
		if is > 0 && d[is-1] == '<' {
			return 1
		}
	}
	return 0
}

func parseValue(d []byte, v int) (int, int, valueKind, bool) {
	if v >= len(d) {
		return 0, 0, 0, false
	}
	if (d[v] == 'r' || d[v] == 'b') && v+1 < len(d) && (d[v+1] == '"' || d[v+1] == '\'') {
		v++
	}
	c := d[v]
	if c == '"' || c == '\'' || c == '`' {
		lim := min(v+1+512, len(d))
		for j := v + 1; j < lim; j++ {
			if d[j] == '\n' {
				break
			}
			if d[j] == '\\' && c != '`' {
				if j+1 < lim && d[j+1] == c {
					return v + 1, j, kindQuoted, true
				}
				j++
				continue
			}
			if d[j] == c {
				return v + 1, j, kindQuoted, true
			}
		}
		return 0, 0, 0, false
	}
	if c == '\\' && v+1 < len(d) && d[v+1] == '"' {
		lim := min(v+2+512, len(d))
		for j := v + 2; j+1 < lim; j++ {
			if d[j] == '\\' && d[j+1] == '"' {
				return v + 2, j, kindQuoted, true
			}
		}
		return 0, 0, 0, false
	}
	j := v
	for j < len(d) && j-v < 1024 {
		c := d[j]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == ',' || c == ';' || c == '"' || c == '\'' || c == '`' || c == '\\' || c == '}' || c == '<' {
			break
		}
		j++
	}
	for j > v && d[j-1] == '.' {
		j--
	}
	return v, j, kindUnquoted, j > v
}

var proseMarkers = [][]byte{[]byte(" is "), []byte(" was "), []byte(": "), []byte(" = "), []byte(" are ")}

var fillerStarts = [][]byte{
	[]byte(" for "), []byte(" to "), []byte(" of "), []byte(" on "), []byte(" at "), []byte(" i "),
	[]byte(" we "), []byte(" you "), []byte(" here"), []byte(" that "), []byte(" which "),
}

func lineEnd(d []byte, from, max int) int {
	lim := min(from+max, len(d))
	if k := bytes.IndexByte(d[from:lim], '\n'); k >= 0 {
		lim = from + k
	}
	if k := bytes.Index(d[from:lim], []byte(`\n`)); k >= 0 {
		lim = from + k
	}
	return lim
}

func (sc *scan) proseAt(ie, minLen int) bool {
	lim := lineEnd(sc.data, ie, 96)
	w := toLower(sc.data[ie:lim])
	best, mlen := -1, 0
	for _, m := range proseMarkers {
		if k := bytes.Index(w, m); k >= 0 && (best < 0 || k < best) {
			best, mlen = k, len(m)
		}
	}
	if best < 0 {
		return false
	}
	filler := w[:best]
	if len(filler) > 0 {
		ok := false
		for _, f := range fillerStarts {
			if bytes.HasPrefix(filler, f) || bytes.Equal(filler, bytes.TrimRight(f, " ")) {
				ok = true
				break
			}
		}
		if !ok || bytes.Contains(filler, []byte(". ")) || bytes.ContainsAny(filler, ",;!?()") {
			return false
		}
	}
	d := sc.data
	v := skipBlank(d, ie+best+mlen)
	j := v
	for j < lim && j-v < 128 && d[j] != ' ' && d[j] != '\t' && d[j] != '\r' && !(d[j] == '\\' && j+1 < lim) {
		j++
	}
	for v < j && bytes.IndexByte([]byte("\"'`*("), d[v]) >= 0 {
		v++
	}
	for j > v && bytes.IndexByte([]byte(".,;:!?)\"'`*\\"), d[j-1]) >= 0 {
		j--
	}
	if j <= v || !plausibleSecret(d[v:j], kindProse, minLen) {
		return false
	}
	sc.add(RuleProse, v, j)
	return true
}

func isEntropyChar(c byte) bool {
	return isAlnum(c) || c == '+' || c == '/' || c == '=' || c == '_' || c == '-'
}

var jsonBoundaries = [][]byte{[]byte(`",`), []byte(`"}`), []byte(`"]`), []byte(`\",`), []byte(`\"}`)}

var idPrefixes = [][]byte{
	[]byte("toolu_"), []byte("srvtoolu_"), []byte("req_"), []byte("msg_"), []byte("call_"),
	[]byte("chatcmpl-"), []byte("resp_"), []byte("run_"), []byte("file_"), []byte("batch_"),
}

func (sc *scan) entropyAt(ie int) {
	d := sc.data
	lim := lineEnd(d, ie, 96)
	for _, b := range jsonBoundaries {
		if k := bytes.Index(d[ie:lim], b); k >= 0 {
			lim = ie + k
		}
	}
	i, tried := ie, 0
	for i < lim && tried < 4 {
		if !isEntropyChar(d[i]) || d[i] == '=' {
			i++
			continue
		}
		j := i
		for j < len(d) && j-i <= 1024 && isEntropyChar(d[j]) {
			j++
		}
		if j-i >= 20 {
			tried++
			start := i
			if k := bytes.IndexByte(d[i:j], '='); k > 0 && k < j-i-2 && isIdentifier(d[i:i+k]) {
				start = i + k + 1
			}
			if entropyCandidate(d[start:j]) {
				sc.add(RuleHighEntropyKeyword, start, j)
				return
			}
		}
		i = j
	}
}

func entropyCandidate(b []byte) bool {
	if len(b) > 1024 || !looksRandom(b) || hasPlaceholder(toLower(b)) {
		return false
	}
	for _, p := range idPrefixes {
		if bytes.HasPrefix(b, p) {
			return false
		}
	}
	u, l, d, _ := classes(b)
	if !d {
		return false
	}
	hex := true
	for _, c := range b {
		if !isHex(c) && c != '-' {
			hex = false
			break
		}
	}
	if (len(b) == 44 && b[43] == '=') || (len(b) == 88 && b[87] == '=') {
		return false
	}
	if !hex && !(u && l) {
		return false
	}
	if hex && (len(b) == 40 || len(b) == 64 || len(b) == 36 || len(b) == 32 || len(b) == 128) {
		return false
	}
	return true
}
