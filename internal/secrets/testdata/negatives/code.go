package auth

type Config struct {
	Token    string `json:"token"`
	Password string `json:"password,omitempty"`
	APIKey   string `toml:"api_key"`
}

func handler(w http.ResponseWriter, r *http.Request) {
	password := r.FormValue("password")
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	key := fmt.Sprintf("%s/%s", bucket, name)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: tok})
	secretKey := cfg.SecretKey
	_ = password
	_ = apiKey
	_ = key
	_ = ts
	_ = secretKey
}
