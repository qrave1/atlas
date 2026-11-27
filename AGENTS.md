# Go development

Use the `golang-how-to` skill for Go tasks and load the relevant Go skills.
Keep small packages and manual constructor injection. The owner user ID is deliberately hardcoded.
Never allow AI output to invoke calendar mutations without confirmation of a stored proposal.
Only events registered locally AND marked with this installation ID may be modified or deleted.
Telegram traffic uses the standard Go HTTP client; do not add application proxy configuration.
Run `go test ./...` and `go vet ./...` after behavior changes.

# Project requirements

The Go module is github.com/qrave1/atlas. Keep internal/planner as the calendar planning package.
Use github.com/caarlos0/env/v11 for configuration and google.golang.org/api/calendar/v3 for Google Calendar.
Dockerfile, docker-compose.yml, and the GHCR build workflow follow qrave1/sozvon, adapted for Atlas.
Ask the owner before adopting additional code or architecture practices from qrave1/sozvon.

Owner-approved conventions: group Config into Telegram, AI, Google, Database and expose config.New().
Use urfave/cli/v3 with one Atlas binary (run and google-auth commands).
Keep database/sql; do not introduce GORM or another ORM.
