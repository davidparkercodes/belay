// Package secrets finds likely credentials in text: known token formats,
// credential assignments in config and code, credentials written in prose,
// and high-entropy values next to a credential keyword. It is a pure function
// over bytes with no I/O, so it can run on every file a projection touches and
// on large agent session logs.
//
// A Finding's Start and End are byte offsets of the secret value itself, so
// data[Start:End] is exactly the span to redact; Line is 1-based. Findings
// never overlap and are sorted by Start. Rules can be switched off by name
// through Options.DisabledRules (for example from "rule:<name>" lines in a
// .belayallow file). Lines longer than Options.DenseLineLength (default 4096)
// with almost no whitespace, such as minified code or base64 blobs, only get
// the token-format rules. Data with a NUL byte in the first
// Options.BinarySniffBytes (default 8000) is treated as binary and skipped.
package secrets

import (
	"bytes"
	"sort"
)

const (
	RuleAnthropicKey       = "anthropic-api-key"
	RuleOpenAIKey          = "openai-api-key"
	RuleGitHubToken        = "github-token"
	RuleGitLabToken        = "gitlab-token"
	RuleAWSAccessKey       = "aws-access-key-id"
	RuleAWSSecretKey       = "aws-secret-access-key"
	RuleStripeKey          = "stripe-secret-key"
	RuleSlackToken         = "slack-token"
	RuleSlackWebhook       = "slack-webhook"
	RuleGoogleAPIKey       = "google-api-key"
	RuleNPMToken           = "npm-token"
	RuleHuggingFaceToken   = "huggingface-token"
	RulePyPIToken          = "pypi-token"
	RuleSendGridKey        = "sendgrid-api-key"
	RuleDigitalOceanToken  = "digitalocean-token"
	RuleShopifyToken       = "shopify-token"
	RulePrivateKey         = "private-key"
	RuleJWT                = "jwt"
	RuleURLCredential      = "url-credentials"
	RuleGoogleAppPassword  = "google-app-password"
	RuleAssignment         = "credential-assignment"
	RuleProse              = "credential-in-prose"
	RuleHighEntropyKeyword = "credential-high-entropy"
)

var allRules = []string{
	RuleAnthropicKey, RuleOpenAIKey, RuleGitHubToken, RuleGitLabToken,
	RuleAWSAccessKey, RuleAWSSecretKey, RuleStripeKey, RuleSlackToken,
	RuleSlackWebhook, RuleGoogleAPIKey, RuleNPMToken, RuleHuggingFaceToken,
	RulePyPIToken, RuleSendGridKey, RuleDigitalOceanToken, RuleShopifyToken,
	RulePrivateKey, RuleJWT, RuleURLCredential, RuleGoogleAppPassword,
	RuleAssignment, RuleProse, RuleHighEntropyKeyword,
}

type Finding struct {
	Rule  string `json:"rule"`
	Line  int    `json:"line"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type Options struct {
	DisabledRules    []string
	DenseLineLength  int
	BinarySniffBytes int
}

type Scanner struct {
	disabled  map[string]bool
	denseLine int
	sniff     int
}

func NewScanner(opts Options) *Scanner {
	s := &Scanner{disabled: map[string]bool{}, denseLine: opts.DenseLineLength, sniff: opts.BinarySniffBytes}
	for _, r := range opts.DisabledRules {
		s.disabled[r] = true
	}
	if s.denseLine <= 0 {
		s.denseLine = 4096
	}
	if s.sniff <= 0 {
		s.sniff = 8000
	}
	return s
}

var defaultScanner = NewScanner(Options{})

func Scan(data []byte) []Finding {
	return defaultScanner.Scan(data)
}

func Rules() []string {
	out := make([]string, len(allRules))
	copy(out, allRules)
	return out
}

func IsRule(name string) bool {
	for _, r := range allRules {
		if r == name {
			return true
		}
	}
	return false
}

func IsBinary(data []byte) bool {
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}

func (s *Scanner) Scan(data []byte) []Finding {
	if len(data) == 0 {
		return nil
	}
	n := len(data)
	if n > s.sniff {
		n = s.sniff
	}
	if bytes.IndexByte(data[:n], 0) >= 0 {
		return nil
	}
	sc := &scan{s: s, data: data}
	sc.scanTokens()
	sc.scanPEM()
	sc.scanURLCredentials()
	if s.heuristicsEnabled() {
		sc.dense = denseRanges(data, s.denseLine)
		anchors := sc.findAnchors()
		sc.scanKeywords(anchors)
		if !s.disabled[RuleGoogleAppPassword] {
			sc.scanAppPasswords(anchors)
		}
	}
	return sc.resolve()
}

func (s *Scanner) heuristicsEnabled() bool {
	return !s.disabled[RuleAssignment] || !s.disabled[RuleProse] || !s.disabled[RuleHighEntropyKeyword] ||
		!s.disabled[RuleGoogleAppPassword] || !s.disabled[RuleAWSSecretKey]
}

func Redact(data []byte, findings []Finding) []byte {
	fs := make([]Finding, len(findings))
	copy(fs, findings)
	sort.Slice(fs, func(i, j int) bool { return fs[i].Start < fs[j].Start })
	out := make([]byte, 0, len(data))
	last := 0
	for _, f := range fs {
		if f.Start < last || f.End > len(data) || f.Start >= f.End {
			continue
		}
		out = append(out, data[last:f.Start]...)
		out = append(out, "[redacted:"...)
		out = append(out, f.Rule...)
		out = append(out, ']')
		last = f.End
	}
	return append(out, data[last:]...)
}

type span struct{ start, end int }

type scan struct {
	s       *Scanner
	data    []byte
	dense   []span
	classes map[string]classResult
	found   []Finding
}

func (sc *scan) add(rule string, start, end int) {
	if sc.s.disabled[rule] || start >= end {
		return
	}
	sc.found = append(sc.found, Finding{Rule: rule, Start: start, End: end})
}

func (sc *scan) inDense(pos int) bool {
	i := sort.Search(len(sc.dense), func(i int) bool { return sc.dense[i].end > pos })
	return i < len(sc.dense) && sc.dense[i].start <= pos
}

func rulePriority(rule string) int {
	switch rule {
	case RuleURLCredential, RuleAWSSecretKey:
		return 1
	case RuleGoogleAppPassword:
		return 2
	case RuleAssignment:
		return 3
	case RuleProse:
		return 4
	case RuleHighEntropyKeyword:
		return 5
	}
	return 0
}

func (sc *scan) resolve() []Finding {
	if len(sc.found) == 0 {
		return nil
	}
	fs := sc.found
	sort.SliceStable(fs, func(i, j int) bool {
		pi, pj := rulePriority(fs[i].Rule), rulePriority(fs[j].Rule)
		if pi != pj {
			return pi < pj
		}
		if fs[i].Start != fs[j].Start {
			return fs[i].Start < fs[j].Start
		}
		return fs[i].End-fs[i].Start > fs[j].End-fs[j].Start
	})
	var kept []Finding
	for _, f := range fs {
		i := sort.Search(len(kept), func(i int) bool { return kept[i].Start >= f.Start })
		if i > 0 && kept[i-1].End > f.Start {
			continue
		}
		if i < len(kept) && kept[i].Start < f.End {
			continue
		}
		kept = append(kept, Finding{})
		copy(kept[i+1:], kept[i:])
		kept[i] = f
	}
	line, pos := 1, 0
	for i := range kept {
		line += bytes.Count(sc.data[pos:kept[i].Start], []byte{'\n'})
		pos = kept[i].Start
		kept[i].Line = line
	}
	return kept
}

func toLower(data []byte) []byte {
	out := make([]byte, len(data))
	for i, c := range data {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return out
}

func denseRanges(data []byte, limit int) []span {
	var out []span
	start := 0
	for start < len(data) {
		end := bytes.IndexByte(data[start:], '\n')
		if end < 0 {
			end = len(data)
		} else {
			end += start
		}
		if end-start > limit {
			line := data[start:end]
			ws := bytes.Count(line, []byte{' '}) + bytes.Count(line, []byte{'\t'})
			lead := bytes.TrimLeft(line, " \t")
			structured := len(lead) > 0 && (lead[0] == '{' || lead[0] == '[')
			if ws*50 < len(line) || (!structured && ws*8 < len(line)) {
				out = append(out, span{start, end})
			}
		}
		start = end + 1
	}
	return out
}
