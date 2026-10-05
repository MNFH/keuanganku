# keuanganku
Personal finance tracker bot for **WhatsApp and Telegram** — track income/expenses/transfers by chat, get PDF recaps on demand.

Your wallets and transactions live in **your own Google Sheet**; the bot just reads and writes it.

## Talking to the bot

Slash commands always work — send `/help` for the full list:
```
/keluar 35rb GoPay Makanan Makan siang
/transfer 500rb BCA GoPay Uang jajan
/rekap bulan lalu pdf
```

With `GEMINI_API_KEY` set you can also just write normally, or photograph a receipt:
```
You:  beli nasi goreng 25rb pakai gopay
Bot:  🔴 Pengeluaran dicatat!
      💳 Dompet: GoPay · 💰 Rp 25.000
      🏷️ Makanan · 📝 nasi goreng

      via: /keluar 25000 GoPay Makanan nasi goreng
      Salah? Kirim /batal
```
Send a photo of a receipt and the bot reads the grand total and the merchant, and logs it as one expense. The command it ran is always echoed back, so a misread is visible immediately — and `/batal` undoes the last entry.

Anything the bot isn't confident about, it asks about rather than guessing, and ordinary conversation is ignored (it has to be safe in group chats).

## Setup

### Prerequisites
- Go 1.25+
- MySQL 8+ (reachable at the host/port in your `MYSQL_DSN`)
- A WhatsApp account to link as the bot
- A Google Cloud service account, for the Google Sheets backend
- (Optional) a Gemini API key, for natural-language and receipt parsing

### 1. Configure environment
```bash
cp .env.example .env
```
Fill in `.env`:
- `MYSQL_DSN` — required, e.g. `user:password@tcp(127.0.0.1:3306)/keuanganku?parseTime=true`
- `GOOGLE_CREDENTIALS_FILE` — path to a Google service-account JSON (defaults to `credentials.json`). Required in practice, since Sheets is where transactions are stored
- `GEMINI_API_KEY` — optional, from [aistudio.google.com](https://aistudio.google.com). Enables free-form messages and receipt photos; without it the bot runs normally but only answers slash commands
- `GEMINI_MODEL` — optional, defaults to `gemini-3.1-flash-lite` (chosen for cost; `gemini-3.8-flash` is more accurate if parsing quality disappoints)
- `TELEGRAM_BOT_TOKEN` — optional, from [@BotFather](https://t.me/BotFather). Serves Telegram alongside WhatsApp
- `DISABLE_WHATSAPP` — optional, set to `1` to run Telegram-only (skips the QR link entirely)
- `ENABLE_MONTHLY_REPORT` — optional, set to `1` for the scheduled monthly PDF. **Off by default**: it is the only unprompted message the bot sends, and on the Cloud API that is billable

### Platforms

Three transports, one brain: the same commands, natural-language parsing, receipt reading and PDF recaps on all of them. Each chat registers its own spreadsheet with `/daftar`, so chats are independent users unless you point them at the same sheet.

| Transport | Enable with | Notes |
|---|---|---|
| WhatsApp (linked device) | on by default; `DISABLE_WHATSAPP=1` to turn off | QR scan, unofficial, no cost |
| Telegram | `TELEGRAM_BOT_TOKEN` | Official, simplest to run |
| WhatsApp Cloud API | the four `WHATSAPP_*` vars | Official, needs a public HTTPS webhook |

At least one must be enabled or startup fails.

### WhatsApp Cloud API setup

The official route. No QR and no ban risk, but more moving parts:

1. **Create the app** at [developers.facebook.com](https://developers.facebook.com) → *Create App* → add the **WhatsApp** product. This also creates a WhatsApp Business Account.
2. **Phone number.** The test number Meta gives you works immediately for development. A production number must **not** be registered on any personal WhatsApp account.
3. **Copy credentials** into `.env`: the access token and `WHATSAPP_PHONE_NUMBER_ID` from the WhatsApp → API Setup page, and `WHATSAPP_APP_SECRET` from App Settings → Basic.
4. **Expose the webhook.** Meta only delivers to a public HTTPS URL, so during development run a tunnel:
   ```bash
   cloudflared tunnel --url http://localhost:8080     # or: ngrok http 8080
   ```
5. **Subscribe.** In WhatsApp → Configuration, set the callback URL to `https://your-tunnel/webhook`, paste the same string you put in `WHATSAPP_VERIFY_TOKEN`, and subscribe to the **messages** field.

**The 24-hour rule.** Meta only allows free-form messages — text and documents alike — within 24 hours of the user's own last message. Everyday use is unaffected and free, because the bot only ever replies to something you sent.

The exception is the scheduled monthly report, which fires unprompted and so lands outside that window. That is why it ships **disabled** (`ENABLE_MONTHLY_REPORT`): enabling it means paying for a template conversation. If you do enable it, create a Utility template, set `WHATSAPP_REENGAGE_TEMPLATE` to its name, and the bot will nudge users to reply rather than silently failing — a template cannot carry the PDF itself, since only the user making contact reopens the window.

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

### Monthly reports (off by default)

Set `ENABLE_MONTHLY_REPORT=1` to have the bot push a PDF recap to every registered chat on the 1st at 07:00 (see `internal/scheduler`).

It is **opt-in** because it is the only message the bot sends unprompted. On the WhatsApp Cloud API that falls outside the free 24-hour service window, so it becomes a billable template conversation. Leaving it off keeps you on the free tier; `/rekap bulanan lalu pdf` still produces the same PDF on demand, free, because it answers a message you sent.
