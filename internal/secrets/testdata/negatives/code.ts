export interface Credentials {
  username: string;
  password: string;
  token?: string;
  apiKey: string | undefined;
}

const apiKey = process.env.OPENAI_API_KEY;
const token = req.headers.authorization?.split(" ")[1];
const secret = import.meta.env.VITE_CLIENT_SECRET;
const password = await getPassword(user.id);
if (password.length < 8) throw new Error("password is too short");
setPassword(e.target.value);
const maxTokens = 4096;
const tokenCount: number = countTokens(prompt);
const authHeader = `Bearer ${token}`;
export function PasswordField() {
  return <input type="password" placeholder="Enter your password" autoComplete="current-password" />;
}
const res = await fetch(url, { headers: { Authorization: `Bearer ${accessToken}` }, credentials: "include" });
