# Atlas — личный AI-помощник в Telegram

## Ручной деплой на VPS

В каталоге приложения (например, `/root/atlas`) должны лежать `docker-compose.yml`, `.env`, `secrets/google-credentials.json` и `google/google-token.json`. Для первого запуска скопируй OAuth-токен из локального `data/google-token.json`. При обновлении используй существующий каталог приложения и серверный токен.

Для запуска и обновления выполни на VPS из каталога приложения:

```bash
docker compose pull && docker compose up -d --remove-orphans
docker compose ps
docker compose logs --tail=40 atlas
```

Параметр `--remove-orphans` убирает старый сервис Watchtower из Compose-проекта Atlas. База хранится в именованном томе `atlas-data`.
