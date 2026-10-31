package secrets

import (
	"bytes"
	"encoding/base64"
)

type tokenRule struct {
	rule     string
	prefixes []string
	body     func(byte) bool
	min, max int
	check    func(body []byte) bool
}

var tokenRules = []tokenRule{
	{RuleAnthropicKey, []string{"sk-ant-"}, isB64URL, 32, 0, randomBody},
	{RuleOpenAIKey, []string{"sk-proj-", "sk-svcacct-", "sk-admin-"}, isB64URL, 32, 0, randomBody},
	{RuleOpenAIKey, []string{"sk-"}, isB64URL, 32, 0, genericOpenAI},
	{RuleGitHubToken, []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_"}, isAlnum, 36, 0, randomBody},
	{RuleGitHubToken, []string{"github_pat_"}, isWord, 50, 0, randomBody},
	{RuleGitLabToken, []string{"glpat-"}, isB64URL, 20, 0, randomBody},
	{RuleNPMToken, []string{"npm_"}, isAlnum, 36, 36, randomBody},
	{RuleHuggingFaceToken, []string{"hf_"}, isAlnum, 30, 40, mixedCaseBody},
	{RuleAWSAccessKey, []string{"AKIA", "ASIA", "ABIA", "ACCA"}, isUpperDigit, 16, 16, randomBody},
	{RuleStripeKey, []string{"sk_live_", "rk_live_"}, isAlnum, 20, 0, randomBody},
	{RuleSlackToken, []string{"xoxb-", "xoxa-", "xoxp-", "xoxr-", "xoxs-", "xoxe-"}, isAlnumDash, 10, 0, slackBody},
	{RuleSlackToken, []string{"xapp-"}, isAlnumDash, 20, 0, slackBody},
	{RuleSlackWebhook, []string{"hooks.slack.com/services/"}, isAlnumSlash, 40, 0, slackWebhookBody},
	{RuleGoogleAPIKey, []string{"AIza"}, isB64URL, 35, 35, randomBody},
	{RulePyPIToken, []string{"pypi-AgEIcHlwaS5vcmc"}, isB64URL, 40, 0, randomBody},
	{RuleSendGridKey, []string{"SG."}, isSendGrid, 66, 66, sendGridBody},
	{RuleDigitalOceanToken, []string{"dop_v1_", "doo_v1_", "dor_v1_"}, isHex, 64, 64, randomBody},
	{RuleShopifyToken, []string{"shpat_", "shpss_", "shpca_", "shppa_"}, isHex, 32, 32, randomBody},
}

type prefixEntry struct {
	prefix []byte
	rule   *tokenRule
}

var prefixTable = func() [256][]prefixEntry {
	var t [256][]prefixEntry
	for i := range tokenRules {
		r := &tokenRules[i]
		for _, p := range r.prefixes {
			t[p[0]] = append(t[p[0]], prefixEntry{[]byte(p), r})
		}
	}
	for c := range t {
		es := t[c]
		for i := 1; i < len(es); i++ {
			for j := i; j > 0 && len(es[j].prefix) > len(es[j-1].prefix); j-- {
				es[j], es[j-1] = es[j-1], es[j]
			}
		}
	}
	return t
}()

var runChar = func() [256]bool {
	var t [256]bool
	for c := 0; c < 256; c++ {
		b := byte(c)
		t[c] = isAlnum(b) || b == '_' || b == '-' || b == '.'
	}
	return t
}()

func (sc *scan) scanTokens() {
	d := sc.data
	n := len(d)
	jwt := !sc.s.disabled[RuleJWT]
	i := 0
	for i < n {
		if !runChar[d[i]] {
			i++
			continue
		}
		start := i
		for i < n && runChar[d[i]] {
			i++
		}
		sc.tokenAt(start, jwt)
		if start >= 1 && d[start-1] == '\\' && (d[start] == 'n' || d[start] == 't' || d[start] == 'r') && start+1 < i {
			sc.tokenAt(start+1, jwt)
		}
	}
}

func (sc *scan) tokenAt(i int, jwt bool) {
	d := sc.data
	if jwt && d[i] == 'e' && i+3 <= len(d) && d[i+1] == 'y' && d[i+2] == 'J' {
		if end := jwtAt(d, i); end > 0 {
			sc.add(RuleJWT, i, end)
			return
		}
	}
	for _, e := range prefixTable[d[i]] {
		if !bytes.HasPrefix(d[i:], e.prefix) {
			continue
		}
		r := e.rule
		if sc.s.disabled[r.rule] {
			continue
		}
		off := i + len(e.prefix)
		j := off
		for j < len(d) && r.body(d[j]) && j-off <= 512 {
			j++
		}
		body := d[off:j]
		if len(body) < r.min || (r.max > 0 && len(body) > r.max) || len(body) > 512 {
			continue
		}
		if r.check != nil && !r.check(body) {
			continue
		}
		sc.add(r.rule, i, j)
		if r.rule == RuleAWSAccessKey {
			sc.awsSecretNear(i, j)
		}
		return
	}
}

func (sc *scan) awsSecretNear(start, end int) {
	if sc.s.disabled[RuleAWSSecretKey] {
		return
	}
	lo, hi := start-400, end+400
	if lo < 0 {
		lo = 0
	}
	if hi > len(sc.data) {
		hi = len(sc.data)
	}
	d := sc.data
	i := lo
	for i < hi {
		if !isAWSSecretChar(d[i]) {
			i++
			continue
		}
		j := i
		for j < len(d) && isAWSSecretChar(d[j]) {
			j++
		}
		if j-i == 40 && tokenBoundaryBefore(d, i) && awsSecretBody(d[i:j]) {
			sc.add(RuleAWSSecretKey, i, j)
		}
		i = j
	}
}

func awsSecretBody(b []byte) bool {
	u, l, dg, _ := classes(b)
	return u && l && dg && randomBody(b)
}

func tokenBoundaryBefore(data []byte, i int) bool {
	if i == 0 {
		return true
	}
	c := data[i-1]
	if isAlnum(c) || c == '_' || c == '-' || c == '.' {
		if (c == 'n' || c == 't' || c == 'r') && i >= 2 && data[i-2] == '\\' {
			return true
		}
		return false
	}
	return true
}

func randomBody(b []byte) bool {
	if hasPlaceholder(toLower(b)) {
		return false
	}
	want := len(b) / 2
	if want > 10 {
		want = 10
	}
	return distinct(b) >= want
}

func genericOpenAI(b []byte) bool {
	for _, p := range []string{"ant-", "proj-", "svcacct-", "admin-"} {
		if bytes.HasPrefix(b, []byte(p)) {
			return false
		}
	}
	u, l, d, _ := classes(b)
	n := 0
	for _, x := range []bool{u, l, d} {
		if x {
			n++
		}
	}
	return n >= 2 && randomBody(b) && lowerRuns(b, 4) <= 2
}

func mixedCaseBody(b []byte) bool {
	u, l, _, _ := classes(b)
	return u && l && randomBody(b)
}

func slackBody(b []byte) bool {
	_, _, d, _ := classes(b)
	return d && randomBody(b)
}

func slackWebhookBody(b []byte) bool {
	return bytes.Count(b, []byte{'/'}) == 2 && randomBody(b)
}

func sendGridBody(b []byte) bool {
	k := bytes.IndexByte(b, '.')
	return k == 22 && bytes.Count(b, []byte{'.'}) == 1 && randomBody(b)
}

func (sc *scan) scanPEM() {
	if sc.s.disabled[RulePrivateKey] {
		return
	}
	data := sc.data
	begin := []byte("-----BEGIN ")
	dashes := []byte("-----")
	off := 0
	for {
		i := bytes.Index(data[off:], begin)
		if i < 0 {
			return
		}
		i += off
		off = i + len(begin)
		lim := off + 64
		if lim > len(data) {
			lim = len(data)
		}
		k := bytes.Index(data[off:lim], dashes)
		if k < 0 {
			continue
		}
		label := data[off : off+k]
		if !bytes.HasSuffix(label, []byte("PRIVATE KEY")) && !bytes.HasSuffix(label, []byte("PRIVATE KEY BLOCK")) {
			continue
		}
		hdrEnd := off + k + len(dashes)
		bodyLim := hdrEnd + 400
		if bodyLim > len(data) {
			bodyLim = len(data)
		}
		run, best := 0, 0
		for _, c := range data[hdrEnd:bodyLim] {
			if isAlnum(c) || c == '+' || c == '/' || c == '=' {
				run++
				if run > best {
					best = run
				}
			} else {
				run = 0
			}
		}
		if best < 40 {
			continue
		}
		end := bodyLim
		endLim := hdrEnd + 64*1024
		if endLim > len(data) {
			endLim = len(data)
		}
		endMarker := []byte("-----END ")
		if e := bytes.Index(data[hdrEnd:endLim], endMarker); e >= 0 {
			e += hdrEnd + len(endMarker)
			if f := bytes.Index(data[e:min(e+64, len(data))], dashes); f >= 0 {
				end = e + f + len(dashes)
			}
		} else {
			end = hdrEnd
			for end < len(data) && end < endLim && (isAlnum(data[end]) || bytes.IndexByte([]byte("+/=\r\n\\:- "), data[end]) >= 0) {
				end++
			}
		}
		sc.add(RulePrivateKey, i, end)
		off = end
	}
}

func jwtAt(data []byte, i int) int {
	a := runOf(data, i, isB64URLPad)
	if a-i < 10 || a >= len(data) || data[a] != '.' {
		return 0
	}
	if !bytes.HasPrefix(data[a+1:], []byte("eyJ")) {
		return 0
	}
	b := runOf(data, a+1, isB64URLPad)
	if b-a-1 < 10 || b >= len(data) || data[b] != '.' {
		return 0
	}
	c := runOf(data, b+1, isB64URL)
	if c-b-1 < 16 {
		return 0
	}
	hdr, err := base64.RawURLEncoding.DecodeString(string(bytes.TrimRight(data[i:a], "=")))
	if err != nil || !bytes.Contains(hdr, []byte(`"alg"`)) {
		return 0
	}
	payload, _ := base64.RawURLEncoding.DecodeString(string(bytes.TrimRight(data[a+1:b], "=")))
	if bytes.Contains(payload, []byte("John Doe")) || bytes.Contains(payload, []byte(`"some":"payload"`)) || bytes.Contains(payload, []byte(`"some": "payload"`)) {
		return 0
	}
	return c
}

func (sc *scan) scanURLCredentials() {
	if sc.s.disabled[RuleURLCredential] {
		return
	}
	data := sc.data
	sep := []byte("://")
	off := 0
	for {
		i := bytes.Index(data[off:], sep)
		if i < 0 {
			return
		}
		i += off
		off = i + 3
		if i == 0 || !isAlpha(data[i-1]) {
			continue
		}
		at := -1
		colon := -1
		for j := off; j < len(data) && j < off+256; j++ {
			c := data[j]
			if c == '@' {
				at = j
				break
			}
			if c == ':' && colon < 0 {
				colon = j
			}
			if c <= ' ' || c == '/' || c == '"' || c == '\'' || c == '<' || c == '>' || c == '\\' || c == '`' {
				break
			}
		}
		if at < 0 || colon < 0 || at+1 >= len(data) || !isAlnum(data[at+1]) {
			continue
		}
		user, pass := data[off:colon], data[colon+1:at]
		if bytes.ContainsAny(pass, ":,") {
			continue
		}
		if bytes.Equal(bytes.ToLower(user), bytes.ToLower(pass)) {
			continue
		}
		if !plausibleSecret(pass, kindQuoted, 6) {
			continue
		}
		sc.add(RuleURLCredential, colon+1, at)
		off = at
	}
}

func runOf(data []byte, i int, f func(byte) bool) int {
	for i < len(data) && f(data[i]) {
		i++
	}
	return i
}

func isAlpha(c byte) bool      { return (c|0x20) >= 'a' && (c|0x20) <= 'z' }
func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isAlnum(c byte) bool      { return isAlpha(c) || isDigit(c) }
func isWord(c byte) bool       { return isAlnum(c) || c == '_' }
func isB64URL(c byte) bool     { return isAlnum(c) || c == '_' || c == '-' }
func isAlnumDash(c byte) bool  { return isAlnum(c) || c == '-' }
func isAlnumSlash(c byte) bool { return isAlnum(c) || c == '/' }
func isUpperDigit(c byte) bool { return (c >= 'A' && c <= 'Z') || isDigit(c) }
func isHex(c byte) bool        { return isDigit(c) || (c|0x20) >= 'a' && (c|0x20) <= 'f' }
func isSendGrid(c byte) bool   { return isB64URL(c) || c == '.' }
func isB64URLPad(c byte) bool {
	return isAlnum(c) || c == '_' || c == '-' || c == '='
}
func isAWSSecretChar(c byte) bool { return isAlnum(c) || c == '/' || c == '+' }
