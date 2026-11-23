# Product — keuanganku

**keuanganku** is a self-hosted Go chat bot for WhatsApp and Telegram that lets an Indonesian user or group record income, expenses and transfers, and get recaps and PDF reports, with all financial data stored in the user's own Google Sheet.

## Core problem
Tracking spending in a spreadsheet is tedious enough that people stop doing it. The bot moves data entry into the chat app they already use: a short slash command, a plain Indonesian sentence or a receipt photo becomes a ledger row. The spreadsheet stays the user's own, so they can open and audit it at any time.

## Key features
- **Registration** — `/daftar <spreadsheet_id>` links the current chat to a sheet shared with the bot's service account. `/hapus` unlinks the chat but never touches the sheet.
- **Recording** — `/masuk <jumlah> <dompet> [kategori] [keterangan]` records income and `/keluar` records an expense. Amounts accept `50000`, `50rb`, `50ribu`, `1.5jt`, `1.5juta` and `200k`. Must be positive.
- **Transfers** — `/transfer <jumlah> <dari> <ke> [keterangan]` moves money between two of the user's own wallets. It writes two linked rows.
- **Wallets** — `/dompet tambah <nama>` adds a wallet and `/dompet list` lists them. Names may contain spaces, must start with a letter or digit and cannot contain `* ? ~`.
- **Balances** — `/saldo` (aliases `/summary`, `/ringkasan`) lists each wallet's balance.
- **Undo** — `/batal` cancels the last active entry, both legs if it was a transfer. The row is marked, never deleted.
- **Recaps** — `/rekap` covers the current or last month, week or day, or a named month or date (`/rekap bulan januari 2025`). Add `pdf` at the end to get a PDF with a pie chart of expenses (at most 6 slices).
- **Natural language** — with `GEMINI_API_KEY` set, free-form text and receipt photos are turned into one of the commands above, which is echoed back as `via: /keluar ...`.
- **Scheduled reports** — opt-in PDF delivered at 07:00: monthly on the 1st (`ENABLE_MONTHLY_REPORT`) and weekly on Monday (`ENABLE_WEEKLY_REPORT`). Off by default.

## Audience
- Primary user: an Indonesian individual tracking personal money. Secondary: a family or group sharing one chat, which shares one sheet.
- All user-facing text is Indonesian (`id`), uses emoji prefixes and WhatsApp `*bold*` markup. There is no other locale.
- Platforms: WhatsApp via whatsmeow (a linked device), Telegram, and the official WhatsApp Cloud API.

## Domain model and rules
- A **chat** (not a sender) maps to one spreadsheet. Several chats may point to the same sheet.
- **Wallet**: a name, an opening balance and a computed balance. **Transaction**: type `income`, `expense` or `transfer` (shown to users as Pemasukan, Pengeluaran, Transfer), with an amount, a wallet, a category, a description, a RefID, a Direction and a Cancelled mark.
- The ledger is append-only. A balance equals the opening balance plus active transactions, computed by a sheet formula and never written by the bot.
- A transfer is neither income nor spending, so recaps exclude transfers.
- Categories from the AI come from a closed list: Makanan, Transport, Belanja, Tagihan, Hiburan, Kesehatan, Pendidikan, Gaji, Bonus, Hadiah, Investasi, Lainnya.
- When unsure, the bot asks rather than guesses, and it stays silent on ordinary chatter because it lives in group chats.

## Out of scope
- No web UI, no multi-currency, no budgets or goals, no bank sync.
- The bot does not edit or delete past rows. Corrections are `/batal` plus a new entry, or a manual edit in the sheet.
- The AI never writes to the sheet directly. It only proposes a command.
