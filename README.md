# Atlas — личный AI-помощник в Telegram

Go-модуль: `github.com/qrave1/atlas`. Личный бот для пользователя **728328832**: голосовые и текст, согласование планов и CRUD собственных событий Google Calendar.

## Правила работы

- Время — `Europe/Moscow`.
- Декомпозиция только по просьбе. Если длительность новой задачи не названа — бот спрашивает.
- При пересечении предлагаются свободные варианты; время не меняется автоматически. Поиск альтернатив: ближайшие 7 дней, 09:00–21:00 МСК, шаг 30 минут.
- Создание, изменение и удаление происходят только по кнопке подтверждения сохранённого плана. Исправление делает предыдущую кнопку недействительной. План действует 24 часа.
- Редактировать и удалять можно только события, ID которых есть в SQLite и которые имеют метку `atlas_owner` этой установки. Чужие события выбранного календаря используются для проверки занятости, но не передаются в AI.
- Повторы операций используют стабильные ID и метку `atlas_operation`; обновление и удаление защищены ETag.
- Ответы текстом; голосовые до 5 минут / 20 МиБ преобразуются FFmpeg в WAV и отправляются в ProxyAPI. Временные файлы удаляются после обработки.

Бот создаёт события с началом и концом, а не Google Tasks. Повторяющиеся события пока не поддерживаются. SQLite хранит диалог, реестр событий, подтверждение, прогресс записи и Telegram offset. Несколько изменений не атомарны: уже выполненные действия сохраняются при частичном сбое.

## Сборка и публикация, как в sozvon

За основу взяты [Dockerfile](https://github.com/qrave1/sozvon/blob/master/Dockerfile), [Compose](https://github.com/qrave1/sozvon/blob/master/docker-compose.yml) и [workflow](https://github.com/qrave1/sozvon/blob/master/.github/workflows/build.yml).

- `.github/workflows/build.yml`: push в `master` / `main` или тега `v*` запускает сборку и публикацию `ghcr.io/qrave1/atlas:latest` через `GITHUB_TOKEN`.
- Dockerfile: Go 1.27 + Alpine 3.24, `CGO_ENABLED=0`, отдельный runtime-образ Alpine. В runtime добавлены CA и FFmpeg; Atlas работает от UID 10001.
- `docker-compose.yml`: готовый GHCR-образ, `restart: always`, `.env`, именованный том `atlas-data`, обновление через Watchtower раз в 300 секунд.
- Watchtower ограничен scope `atlas`. Сетевые настройки Traefik и TURN из sozvon не нужны боту с long polling, поэтому не перенесены.

Workflow подготовлен локально. Чтобы появился образ, разместите код в `github.com/qrave1/atlas` и выполните push. Для простого запуска сделайте пакет `ghcr.io/qrave1/atlas` публичным в настройках GitHub Packages. Для приватного пакета потребуется отдельная авторизация Docker и Watchtower в GHCR.

Никакие ключи, Google JSON или база не должны попадать в Git или Docker-образ: `.gitignore` и `.dockerignore` исключают соответствующие каталоги.

## Один раз подключить Google Calendar

Нужен OAuth client типа **Desktop app**, включённый Google Calendar API и авторизация на компьютере владельца.

Для использования только своим аккаунтом можно создать [Personal mode проект](https://console.cloud.google.com/auth/overview/personal), выбрать Desktop app и скачать JSON. В этом режиме авторизация доступна только создателю, без OAuth-верификации и семидневного ограничения тестовых токенов. Существующий External-проект нельзя переключить в Personal — нужен новый проект. [Документация Google](https://support.google.com/cloud/answer/15544987?hl=en).

Если используете External / Testing, добавьте свой аккаунт в Test users. Для Calendar refresh token в таком режиме истекает через 7 дней. [Ограничения Google](https://support.google.com/cloud/answer/15549945?hl=en).

1. Сохраните Desktop client JSON как `secrets/google-credentials.json`.
2. На своём компьютере из папки проекта выполните:

   ```sh
   go run ./cmd/google-auth
   ```

   Или в Windows используйте собранный `bin/google-auth.exe`.
3. Откройте напечатанную ссылку на том же компьютере, выберите нужный Google-аккаунт и разрешите доступ.
4. Результат — `data/google-token.json`. Данные авторизации не выводятся в чат. Команда слушает `127.0.0.1:8765`, использует state и PKCE и завершается после авторизации.

Для запуска авторизации нужен Go 1.27+. В этом рабочем окружении доступен `.tools/go/bin/go.exe`. Google OAuth-клиент запрашивает scope `calendar.events`; более узкое ограничение «только события бота» проверяется кодом приложения.

## Настройки

Скопируйте `.env.example` в `.env` и заполните:

```dotenv
TELEGRAM_BOT_TOKEN=YOUR_BOT_TOKEN
TELEGRAM_PROXY_URL=socks5://USER:PASSWORD@PROXY_HOST:1080
AI_API_KEY=YOUR_PROXYAPI_KEY
AI_BASE_URL=https://api.proxyapi.ru/v1
AI_CHAT_MODEL=openai/gpt-5-mini
AI_TRANSCRIBE_MODEL=openai/gpt-4o-mini-transcribe
GOOGLE_CALENDAR_ID=primary
```

Конфиг читается `github.com/caarlos0/env/v11`, с `env` / `envDefault` тегами и проверкой обязательных ключей. `.env` загружает Docker Compose; нативному Go-процессу нужно передать переменные окружения.

Прокси используется только для Telegram, включая скачивание файлов. Google и ProxyAPI идут напрямую, независимо от глобальных proxy env-переменных. Поддерживаются `socks5`, `socks5h`, `http`, `https`; в логине и пароле нужно URL-кодировать спецсимволы. Прокси должен быть доступен из контейнера; `127.0.0.1` внутри Docker означает сам контейнер.

## Деплой одной командой с Windows

На VPS нужны Linux, SSH, Bash, Docker Engine и Compose plugin. Для первого запуска используйте root либо пользователя с беспарольным sudo, чтобы выставить владельца Google-файлов (UID 10001). Go и исходники на сервере не нужны: приложение скачивается из GHCR.

До первого деплоя:

1. Выполните push и дождитесь успешного workflow `build-and-push` в GitHub Actions.
2. Проверьте доступность образа `ghcr.io/qrave1/atlas:latest`.
3. Подготовьте локальные `.env`, `secrets/google-credentials.json` и `data/google-token.json`.

Первый запуск из PowerShell:

```powershell
.\scripts\deploy.ps1 -Server root@VPS_IP -Bootstrap
```

Скрипт загрузит Compose и конфигурацию в `~/atlas`, поместит Google-токен в `~/atlas/google/google-token.json`, скачает образы и запустит Atlas с Watchtower. Секреты передаются только с явным `-Bootstrap`; при наличии конфигурации на сервере этот режим откажется её перезаписывать.

Обновление конфигурации Compose вручную или повторное развёртывание:

```powershell
.\scripts\deploy.ps1 -Server root@VPS_IP
```

В этом режиме секреты не загружаются. Для обычного обновления самого Go-кода достаточно push в GitHub: после успешной публикации образа Watchtower загрузит новую версию.

Дополнительные параметры:

```powershell
.\scripts\deploy.ps1 -Server user@VPS_IP -Port 2222 -IdentityFile "$env:USERPROFILE\.ssh\id_ed25519"
```

`-Directory имя` задаёт каталог в домашней папке пользователя на VPS. Сохраняйте одно имя каталога при следующих деплоях: оно определяет Compose-проект и имя тома. Не запускайте два экземпляра с одним Telegram-токеном.

Если PowerShell запрещает запуск локального скрипта:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\deploy.ps1 -Server root@VPS_IP -Bootstrap
```

Временный архив локально удаляется после выполнения. На VPS архив удаляется при завершении SSH-скрипта. Скрипт сначала скачивает образы; если скачивание не удалось, работающий контейнер не пересоздаётся. Registry и сеть VPS должны позволять скачать GHCR-образы; `TELEGRAM_PROXY_URL` не применяется к Docker.

## Данные на VPS

| Данные | Место хранения |
|---|---|
| SQLite | Именованный том `atlas-data`, путь `/app/data/atlas.db` в контейнере |
| Google credentials | `~/atlas/secrets/google-credentials.json`, каталог подключён read-only |
| Google refresh token | `~/atlas/google/google-token.json`, каталог доступен для обновления токена |
| Настройки | `~/atlas/.env` |

Для Google-токена подключён каталог, а не отдельный файл: сохранение после refresh использует атомарную замену файла. База и installation ID сохраняются при пересоздании контейнера. **Не выполняйте `docker compose down -v`**, если нужно сохранить реестр событий и диалоги.

Для согласованной резервной копии остановите Atlas, сохраните содержимое именованного тома вместе с `google`, `secrets` и `.env`, затем запустите Atlas. SQLite работает в WAL-режиме: нельзя копировать только `atlas.db`, игнорируя активный WAL. Потеря базы означает потерю реестра; старая установка автоматически не восстанавливается по меткам событий.

## Управление и проверка

На VPS из `~/atlas`:

```sh
docker compose ps
docker compose logs --tail=100 -f atlas
```

В Telegram с аккаунта `728328832` отправьте `/start`, затем «Сделать практику завтра в 12:00». Бот должен спросить длительность и показать план или альтернативы. До нажатия кнопки события в календаре нет. После подтверждения проверьте `/tasks`, затем попробуйте перенос и удаление с отдельным подтверждением.

| Команда | Действие |
|---|---|
| `/start`, `/help` | Подсказка |
| `/tasks` | До 50 собственных событий: последние 7 дней и будущее |
| `/cancel` | Отмена ещё не начатого плана и очистка диалога |
| `/resume` | Продолжение уже подтверждённой записи после сбоя |
| `/discard` | Остановка оставшихся действий; уже выполненное сохраняется |

При частичной записи используйте `/resume` либо `/discard`. После `/discard` проверьте `/tasks`: Google мог выполнить запрос, ответ на который потерялся.

## Разработка

```sh
go test ./...
go vet ./...
go build ./cmd/atlas ./cmd/google-auth
```

Для локальных голосовых нужен FFmpeg в PATH. В Docker он уже установлен.

Компоненты связываются обычными конструкторами. `internal/planner` отвечает за согласование и логику календаря; остальные пакеты — `bot`, `ai`, `calendar`, `store`, `config`, `model`.

Calendar использует официальный `google.golang.org/api/calendar/v3`; OAuth — `golang.org/x/oauth2`; конфиг — `github.com/caarlos0/env/v11`; SQLite — `modernc.org/sqlite`. Telegram и ProxyAPI используют HTTP-клиенты.

Тесты работают с временным SQLite и локальными HTTP/SOCKS5-серверами, без настоящих API-ключей. Покрыты доступ по userID, подтверждения, истечение плана, владение событиями, конфликты, ETag, повтор после сбоя, JSON/multipart ProxyAPI, пагинация официального SDK и конфигурация env.

Ограничения: до 10 изменений за подтверждение, длительность события до 24 часов, планирование до года вперёд, до 300 событий в AI-контексте и последние 24 сообщения истории. Проверяйте даты, время и описание перед подтверждением.
