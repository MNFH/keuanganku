# Tech stack

## Language and runtime
- **Go 1.25** (`go.mod`), module `github.com/nurfaizh/keuanganku`. A single binary built from `cmd/api`.
- **github.com/joho/godotenv v1.5.1**: `config/config.go` loads `.env`. `log.Fatalf` is called when `MYSQL_DSN` is missing.

## Storage
- **Google Sheets** via `google.golang.org/api v0.291.0` (`sheets/v4`) is the transaction database, authenticated by the service-account JSON at `GOOGLE_CREDENTIALS_FILE`.
- **MySQL 8+** via `github.com/go-sql-driver/mysql v1.10.0` holds the `users` (chat → spreadsheet) and `report_deliveries` tables, both auto-created. The database itself must already exist.
- **SQLite** via `modernc.org/sqlite v1.55.0` stores the whatsmeow session in `whatsmeow.db`.

## Messaging
- **go.mau.fi/whatsmeow** (a pseudo-version pinned in go.mod) runs the linked-device WhatsApp client. `github.com/mdp/qrterminal/v3 v3.2.1` prints the pairing QR.
- Telegram (long-polling Bot API) and the WhatsApp Cloud API (Graph `v26.0` by default) are called with plain `net/http`, without wrapper libraries.

## AI and reports
- **google.golang.org/genai v1.72.0** provides Gemini intent parsing in `internal/ai`. The default model is `gemini-3.1-flash-lite` (`GEMINI_MODEL` overrides it). go.mod lists it as `// indirect` although it is imported directly; `go mod tidy` would fix this.
- **github.com/jung-kurt/gofpdf v1.16.2** builds the PDF and **github.com/wcharczuk/go-chart/v2 v2.1.2** draws the pie chart.
- **github.com/google/uuid v1.6.0** generates the transfer RefIDs.

## Commands
```bash
go build -o keuanganku.exe ./cmd/api        # build the bot
go run ./cmd/api                            # run it (needs .env, MySQL and credentials.json)
go vet ./...                                # static checks; currently clean
go test ./...                               # unit tests; none of them touch the network
go test ./internal/model -run TestFormatAmount -v   # run one test
go test -tags live ./internal/sheets        # live Sheets test, needs a BLANK shared sheet
go run ./cmd/smoke                          # live Gemini check (add -image); uses quota
go run ./cmd/tgsmoke                        # manual Telegram transport check
go run ./cmd/wacsmoke                       # manual WhatsApp Cloud API check
gofmt -d <file>                             # check one file's formatting
```

## Key config files
- `.env` / `.env.example`: every environment variable, including `MYSQL_DSN`, `GEMINI_API_KEY`, `TELEGRAM_BOT_TOKEN`, `DISABLE_WHATSAPP`, the four `WHATSAPP_*` variables, `WEBHOOK_ADDR`/`WEBHOOK_PATH` and the `ENABLE_*_REPORT` flags.
- `config/config.go` turns the environment into a `Config` struct. Defaults live here.
- `CLAUDE.md` / `AGENTS.md` hold the architecture notes for agents.

## Conventions
- Tests sit beside the code in table-driven `_test.go` files and cover only dependency-free logic.
- Errors shown to users are Indonesian strings. Internal detail goes to `log.Printf`.
- Dates are written as `2006-01-02 15:04:05` local time and read with `time.ParseInLocation(..., time.Local)`.

## Do not
- Do not run `gofmt -w .`: the checkout uses CRLF line endings, so every file would be reformatted and real changes would get lost in the diff.
- Do not add an ORM or a migration tool. Tables are created with `CREATE TABLE IF NOT EXISTS` in `userstore` and `scheduler`.
- Do not add a Telegram or Graph SDK. Follow the existing plain-HTTP clients.
- Do not use `time.Parse` for sheet dates, because it assumes UTC.
- Do not commit `.env`, `credentials.json`, `gsm.json`, `users.json` or `whatsmeow.db*`.
