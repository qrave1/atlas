# Atlas — личный AI-помощник в Telegram

Первоначальное подключение Google Calendar выполняется командой `google-auth` на компьютере с браузером. Без команды Atlas запускается по умолчанию:

```powershell
go run . google-auth
go run .
```

Пути задаются через `GOOGLE_CREDENTIALS_FILE` и `GOOGLE_TOKEN_FILE` либо флагами `--credentials` и `--token`:

```powershell
go run . google-auth --credentials .\secrets\google-credentials.json --token .\secrets\google-token.json
```
