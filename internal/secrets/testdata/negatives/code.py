import os
from typing import Optional

password = os.getenv("DB_PASSWORD")
api_key = os.environ["OPENAI_API_KEY"]
SECRET_KEY = env("SECRET_KEY")
token: Optional[str] = None


def verify_token(token: str) -> bool:
    return token == settings.API_TOKEN


response = client.messages.create(model=model, max_tokens=1024, messages=messages)
tokenizer = AutoTokenizer.from_pretrained("bert-base-uncased")
password_hash = bcrypt.hashpw(password.encode(), bcrypt.gensalt())
PASSWORD_MIN_LENGTH = 12
secret_name = "prod/db/credentials"
auth_token_header = "X-Auth-Token"
credentials = service_account.Credentials.from_service_account_file(path)
