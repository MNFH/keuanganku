# keuanganku
Personal finance tracker bot over WhatsApp — track income/expenses/transfers by chat, get PDF recaps and monthly reports.

## Setup

### Prerequisites
- Go 1.25+
- MySQL 8+ (reachable at the host/port in your `MYSQL_DSN`)
- A WhatsApp account to link as the bot
- (Optional) a Google Cloud service account for Google Sheets export

### 1. Configure environment
```bash
cp .env.example .env
```
Fill in `.env`:
- `MYSQL_DSN` — required, e.g. `user:password@tcp(127.0.0.1:3306)/keuanganku?parseTime=true`
- `GOOGLE_CREDENTIALS_FILE` — optional, path to a Google service-account JSON (defaults to `credentials.json`), only needed for the Sheets export feature

### 2. Create the database
The `users` table is auto-migrated on startup, but the schema itself must exist first:
```sql
CREATE DATABASE keuanganku;
```

### 3. Build and run
```bash
go build -o keuanganku.exe ./cmd/api
./keuanganku.exe
```
On first run, scan the printed QR code with WhatsApp → **Settings → Linked Devices → Link a Device**. The session is then persisted locally in `whatsmeow.db`, so future restarts reconnect automatically without rescanning.

The bot also sends an automatic monthly PDF report to every registered chat (see `internal/scheduler`).
