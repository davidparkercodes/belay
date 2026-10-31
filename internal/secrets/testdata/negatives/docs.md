# Configuration

Set `ANTHROPIC_API_KEY` to your key, then run `example login`.
The token budget is 2048 tokens per request.
Pass `--token` to override the token from the environment.

Example JWT from the spec:

    eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c

| Setting | Description |
|---|---|
| `password` | Database password. Required. |
| `token_url` | https://auth.example.com/oauth/token |

The public key looks like this:

    -----BEGIN PUBLIC KEY-----
    MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEexampleexampleexampleexampleexample
    -----END PUBLIC KEY-----
