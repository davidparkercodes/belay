package secrets

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"hash/fnv"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	alnum      = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	upperDigit = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	hexLower   = "0123456789abcdef"
	b64url     = alnum + "-_"
	b64std     = alnum + "+/"
	digits     = "0123456789"
)

func randFrom(r *rand.Rand, alphabet string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return string(b)
}

func until(r *rand.Rand, gen func() string, ok func([]byte) bool) string {
	for {
		s := gen()
		if ok([]byte(s)) {
			return s
		}
	}
}

func pem(kind string, r *rand.Rand) string {
	var sb strings.Builder
	sb.WriteString("-----" + "BEGIN " + kind + " PRIVATE" + " KEY-----\n")
	for i := 0; i < 6; i++ {
		sb.WriteString(randFrom(r, b64std, 64) + "\n")
	}
	sb.WriteString("-----" + "END " + kind + " PRIVATE" + " KEY-----")
	return sb.String()
}

func jwtFor(r *rand.Rand) string {
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." +
		enc.EncodeToString([]byte(`{"sub":"user-42","iat":1700000000}`)) + "." + randFrom(r, b64url, 43)
}

var generators = map[string]func(r *rand.Rand) string{
	"anthropic": func(r *rand.Rand) string { return "sk" + "-ant-api03-" + randFrom(r, b64url, 93) + "AA" },
	"openai_proj": func(r *rand.Rand) string {
		return "sk" + "-proj-" + randFrom(r, b64url, 120)
	},
	"openai_legacy": func(r *rand.Rand) string { return "sk" + "-" + randFrom(r, alnum, 48) },
	"ghp":           func(r *rand.Rand) string { return "gh" + "p_" + randFrom(r, alnum, 36) },
	"gho":           func(r *rand.Rand) string { return "gh" + "o_" + randFrom(r, alnum, 36) },
	"ghu":           func(r *rand.Rand) string { return "gh" + "u_" + randFrom(r, alnum, 36) },
	"ghs":           func(r *rand.Rand) string { return "gh" + "s_" + randFrom(r, alnum, 36) },
	"ghr":           func(r *rand.Rand) string { return "gh" + "r_" + randFrom(r, alnum, 36) },
	"github_pat": func(r *rand.Rand) string {
		return "github" + "_pat_" + randFrom(r, alnum, 22) + "_" + randFrom(r, alnum, 59)
	},
	"glpat": func(r *rand.Rand) string { return "gl" + "pat-" + randFrom(r, b64url, 20) },
	"npm":   func(r *rand.Rand) string { return "np" + "m_" + randFrom(r, alnum, 36) },
	"hf": func(r *rand.Rand) string {
		return until(r, func() string { return "h" + "f_" + randFrom(r, alnum, 34) }, func(b []byte) bool { return mixedCaseBody(b[3:]) })
	},
	"aws_id": func(r *rand.Rand) string { return "AK" + "IA" + randFrom(r, upperDigit, 16) },
	"aws_secret": func(r *rand.Rand) string {
		return until(r, func() string { return randFrom(r, b64std, 40) }, awsSecretBody)
	},
	"stripe_sk": func(r *rand.Rand) string { return "sk" + "_live_" + randFrom(r, alnum, 24) },
	"stripe_rk": func(r *rand.Rand) string { return "rk" + "_live_" + randFrom(r, alnum, 24) },
	"slack_bot": func(r *rand.Rand) string {
		return "xo" + "xb-" + randFrom(r, digits, 12) + "-" + randFrom(r, digits, 13) + "-" + randFrom(r, alnum, 24)
	},
	"slack_webhook": func(r *rand.Rand) string {
		return "https://hooks.slack.com/" + "services/T" + randFrom(r, upperDigit, 10) + "/B" + randFrom(r, upperDigit, 10) + "/" + randFrom(r, alnum, 24)
	},
	"google":       func(r *rand.Rand) string { return "AI" + "za" + randFrom(r, b64url, 35) },
	"pypi":         func(r *rand.Rand) string { return "pypi-" + "AgEIcHlwaS5vcmc" + randFrom(r, b64url, 70) },
	"sendgrid":     func(r *rand.Rand) string { return "SG" + "." + randFrom(r, b64url, 22) + "." + randFrom(r, b64url, 43) },
	"digitalocean": func(r *rand.Rand) string { return "do" + "p_v1_" + randFrom(r, hexLower, 64) },
	"shopify":      func(r *rand.Rand) string { return "sh" + "pat_" + randFrom(r, hexLower, 32) },
	"pem_rsa":      func(r *rand.Rand) string { return pem("RSA", r) },
	"pem_openssh":  func(r *rand.Rand) string { return pem("OPENSSH", r) },
	"jwt":          jwtFor,
	"rand32": func(r *rand.Rand) string {
		return until(r, func() string { return randFrom(r, alnum, 32) }, entropyCandidate)
	},
}

var templateRe = regexp.MustCompile(`\{\{([a-z_0-9]+)\}\}`)

type corpusCase struct {
	name    string
	rules   []string
	text    string
	secrets []string
}

func expand(t *testing.T, text string, seed string) (string, []string) {
	h := fnv.New64a()
	h.Write([]byte(seed))
	r := rand.New(rand.NewSource(int64(h.Sum64())))
	var values []string
	out := templateRe.ReplaceAllStringFunc(text, func(m string) string {
		name := templateRe.FindStringSubmatch(m)[1]
		g, ok := generators[name]
		if !ok {
			t.Fatalf("unknown template %s", name)
		}
		v := g(r)
		values = append(values, v)
		return v
	})
	return out, values
}

func loadPositives(t *testing.T) []corpusCase {
	f, err := os.Open("testdata/positives.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var cases []corpusCase
	var cur *corpusCase
	var body []string
	flush := func() {
		if cur == nil {
			return
		}
		text := strings.TrimRight(strings.Join(body, "\n"), "\n")
		expanded, values := expand(t, text, cur.name)
		cur.text = expanded
		cur.secrets = append(cur.secrets, values...)
		cases = append(cases, *cur)
		cur, body = nil, nil
	}
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "=== "):
			flush()
			n++
			var rules []string
			for _, r := range strings.Split(strings.TrimPrefix(line, "=== "), ",") {
				rules = append(rules, strings.TrimSpace(r))
			}
			sort.Strings(rules)
			cur = &corpusCase{name: strings.Join(rules, ",") + "#" + itoa(n), rules: rules}
		case cur == nil:
		case strings.HasPrefix(line, "@secret "):
			cur.secrets = append(cur.secrets, strings.TrimPrefix(line, "@secret "))
		default:
			body = append(body, line)
		}
	}
	flush()
	return cases
}

func itoa(n int) string { return strconv.Itoa(n) }

func ruleSet(fs []Finding) []string {
	m := map[string]bool{}
	for _, f := range fs {
		m[f.Rule] = true
	}
	var out []string
	for r := range m {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func describe(data []byte, fs []Finding) string {
	var sb strings.Builder
	for _, f := range fs {
		sb.WriteString("\n  " + f.Rule + " line " + itoa(f.Line) + ": " + string(data[f.Start:f.End]))
	}
	return sb.String()
}

func TestPositives(t *testing.T) {
	cases := loadPositives(t)
	if len(cases) < 40 {
		t.Fatalf("only %d positive cases loaded", len(cases))
	}
	for _, c := range cases {
		for _, r := range c.rules {
			if !IsRule(r) {
				t.Fatalf("%s: unknown rule %q", c.name, r)
			}
		}
		data := []byte(c.text)
		fs := Scan(data)
		got := ruleSet(fs)
		if strings.Join(got, ",") != strings.Join(c.rules, ",") {
			t.Errorf("%s: want rules %v, got %v%s\ntext: %s", c.name, c.rules, got, describe(data, fs), c.text)
			continue
		}
		red := Redact(data, fs)
		for _, s := range c.secrets {
			if bytes.Contains(red, []byte(s)) {
				t.Errorf("%s: secret %q survived redaction: %s", c.name, s, red)
			}
		}
		for _, f := range fs {
			if !bytes.Contains(red, []byte("[redacted:"+f.Rule+"]")) {
				t.Errorf("%s: missing redaction marker for %s", c.name, f.Rule)
			}
		}
	}
}

func generatedNegatives() map[string][]byte {
	r := rand.New(rand.NewSource(7))
	img := base64.StdEncoding.EncodeToString([]byte(randFrom(r, alnum+"\x00\x01\xff", 60000)))
	min := strings.Repeat(`function(e){var t=e.token||"",n=e.password;return t&&n?{token:t,key:e.apiKey,auth:"Bearer "+t}:null};`, 400)
	return map[string][]byte{
		"base64 image in JSON":    []byte(`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + img + `"}}`),
		"long minified bundle":    []byte(min),
		"stripe placeholder":      []byte("STRIPE_KEY=sk" + "_live_" + strings.Repeat("x", 24)),
		"github placeholder":      []byte("GH_TOKEN=gh" + "p_" + strings.Repeat("X", 36)),
		"anthropic placeholder":   []byte("ANTHROPIC_API_KEY=sk" + "-ant-" + strings.Repeat("x", 40)),
		"aws docs example pair":   []byte("AK" + "IAIOSFODNN7EXAMPLE wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
		"binary with token bytes": append([]byte{0, 1, 2, 0}, []byte("gh"+"p_"+randFrom(r, alnum, 36))...),
		"lockfile hashes":         []byte(strings.Repeat("checksum = \""+randFrom(r, hexLower, 64)+"\"\n", 50)),
		"git shas":                []byte(strings.Repeat(randFrom(r, hexLower, 40)+" token refresh\n", 50)),
	}
}

func TestNegatives(t *testing.T) {
	files, err := filepath.Glob("testdata/negatives/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 10 {
		t.Fatalf("only %d negative files", len(files))
	}
	cases := 0
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		cases += bytes.Count(data, []byte{'\n'})
		if fs := Scan(data); len(fs) > 0 {
			t.Errorf("%s: %d false positives%s", path, len(fs), describe(data, fs))
		}
	}
	for name, data := range generatedNegatives() {
		cases++
		if fs := Scan(data); len(fs) > 0 {
			t.Errorf("%s: %d false positives%s", name, len(fs), describe(data, fs))
		}
	}
	t.Logf("negative files: %d, negative lines/cases: %d", len(files), cases)
}

func TestDisabledRules(t *testing.T) {
	data := []byte("DB_PASSWORD=Tr0ub4dor&3\nGH=gh" + "p_" + "aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY3zA5")
	if got := ruleSet(Scan(data)); len(got) != 2 {
		t.Fatalf("want 2 rules, got %v", got)
	}
	s := NewScanner(Options{DisabledRules: []string{RuleGitHubToken}})
	got := ruleSet(s.Scan(data))
	if len(got) != 1 || got[0] != RuleAssignment {
		t.Fatalf("want only assignment with github disabled, got %v", got)
	}
}

func TestFindingOffsetsAndLines(t *testing.T) {
	data := []byte("line one\nline two\nmy password for the router is Tr0ub4dor&3\n")
	fs := Scan(data)
	if len(fs) != 1 {
		t.Fatalf("want 1 finding, got %d", len(fs))
	}
	f := fs[0]
	if f.Line != 3 || string(data[f.Start:f.End]) != "Tr0ub4dor&3" {
		t.Fatalf("got line %d span %q", f.Line, data[f.Start:f.End])
	}
	want := "line one\nline two\nmy password for the router is [redacted:credential-in-prose]\n"
	if got := string(Redact(data, fs)); got != want {
		t.Fatalf("redact: %q", got)
	}
}

func TestRulesStable(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Rules() {
		if seen[r] {
			t.Fatalf("duplicate rule %s", r)
		}
		seen[r] = true
		if !IsRule(r) {
			t.Fatalf("IsRule(%s) false", r)
		}
	}
	if IsRule("nope") {
		t.Fatal("IsRule accepted unknown name")
	}
}

func TestAppPasswordCommonWords(t *testing.T) {
	for _, s := range []string{
		"the password: that will make sure",
		"password reset: this code runs fast",
		"token note: user must have more time",
	} {
		if fs := Scan([]byte(s)); len(fs) > 0 {
			t.Errorf("%q: unexpected %v", s, ruleSet(fs))
		}
	}
}
