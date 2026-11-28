# Atlas — личный AI-помощник в Telegram

Atlas создаёт и управляет задачами в списке Google Tasks `@default`. Календарная интеграция оставлена в коде, но сейчас бот её не запускает.

Перед первым запуском включите Google Tasks API в Google Cloud. Затем выполните OAuth-команду на компьютере с браузером; если раньше Atlas был подключён к Google Calendar, выполните её повторно, чтобы выдать доступ к Tasks. Без команды Atlas запускается по умолчанию:

```powershell
go run . google-auth
go run .
```

OAuth выдаёт доступ к Google Tasks. Повторный запуск `google-auth` обновит сохранённый токен. Пути задаются через `GOOGLE_CREDENTIALS_FILE` и `GOOGLE_TOKEN_FILE` либо флагами `--credentials` и `--token`:

```powershell
go run . google-auth --credentials .\secrets\google-credentials.json --token .\secrets\google-token.json
```

Для другой task list задайте `GOOGLE_TASK_LIST_ID`; по умолчанию используется `@default`. Google Tasks хранит срок задачи как дату без времени суток. Если в сообщении указано время, Atlas уточнит, подходит ли срок только датой.
