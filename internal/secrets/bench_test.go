package secrets

import (
	"math/rand"
	"strings"
	"testing"
)

func benchCorpus() []byte {
	r := rand.New(rand.NewSource(1))
	var sb strings.Builder
	prose := []string{
		"I updated the config loader so the token refresh happens before expiry.",
		"Run the tests again; the password field validation still fails on empty input.",
		"Here is the diff for the auth middleware and the new key rotation helper.",
		"func handler(w http.ResponseWriter, r *http.Request) { token := r.Header.Get(\"Authorization\") }",
		"const apiKey = process.env.API_KEY; if (!apiKey) throw new Error(\"missing key\");",
	}
	for sb.Len() < 4<<20 {
		sb.WriteString(`{"type":"assistant","uuid":"`)
		sb.WriteString(randFrom(r, hexLower, 32))
		sb.WriteString(`","message":{"id":"msg_`)
		sb.WriteString(randFrom(r, alnum, 24))
		sb.WriteString(`","content":[{"type":"text","text":"`)
		for i := 0; i < 8; i++ {
			sb.WriteString(prose[r.Intn(len(prose))])
			sb.WriteString(`\n`)
		}
		sb.WriteString(`"}]}}` + "\n")
		sb.WriteString("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")
	}
	return []byte(sb.String())
}

func BenchmarkScanKeywordDense(b *testing.B) {
	data := benchCorpus()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Scan(data)
	}
}

func BenchmarkScanMinified(b *testing.B) {
	r := rand.New(rand.NewSource(2))
	data := []byte(strings.Repeat(`function(e){var t=e.token||"",n=e.password;return t&&n?{token:t,key:e.apiKey,id:"`+randFrom(r, alnum, 16)+`"}:null};`, 40000))
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Scan(data)
	}
}

func BenchmarkScanBase64(b *testing.B) {
	r := rand.New(rand.NewSource(3))
	data := []byte(`{"data":"` + randFrom(r, b64std, 4<<20) + `"}`)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Scan(data)
	}
}
