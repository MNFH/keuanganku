# Requirements Document

## Introduction

Today a registered chat can record income, expenses and transfers, see wallet balances and get period totals, but it cannot see individual transactions. To check what was recorded, users must open the Google Sheet. This matters most before Undo_Command: it cancels the latest active transaction, and the user cannot see in the chat which one that is.

Transaction History adds a History_Command that lists the chat's most recent Active_Transactions in the chat, newest first. It can be limited to a chosen count, to one Period or to one Wallet, and it shows each Transfer once rather than as its two internal rows. It only reads data and never changes the ledger.

## Glossary

- **Chat**: a WhatsApp or Telegram conversation (private or group) in which the bot receives messages.
- **Registered_Chat**: a Chat linked to a spreadsheet through the registration command.
- **Ledger**: the transactions stored in the spreadsheet of a Registered_Chat, shared by every Chat linked to the same spreadsheet.
- **Active_Transaction**: an income, expense or transfer in the Ledger that has not been cancelled by Undo_Command.
- **Cancelled_Transaction**: a transaction in the Ledger that Undo_Command has marked as cancelled; it stays in the spreadsheet as history.
- **Transfer**: one movement of money between two of the user's Wallets, recorded with an amount, a source Wallet, a destination Wallet and a description.
- **Wallet**: a named account of the user, such as BCA or GoPay, as registered in the Ledger.
- **History_Command**: the `/riwayat` slash command, or its identical alias `/history`, with an optional count, an optional Period and an optional Wallet name, in that order.
- **Period**: a time window named with the same words the recap command accepts: today, yesterday, this week, last week, this month or last month.
- **History_List**: the bot's reply to History_Command: a header, then one History_Entry per line group, newest first.
- **History_Entry**: one item in the History_List describing a single Active_Transaction or a single Transfer.
- **Default_Count**: 10, the largest number of History_Entry lines shown when the user gives no count.
- **Max_Count**: 30, the largest number of History_Entry lines one History_List may show.
- **Undo_Command**: the existing command that cancels the latest Active_Transaction, both sides of it when it is a Transfer.
- **Help_Message**: the existing command list the bot sends in reply to the help command.
- **Natural_Language_Input**: a free-form message without a leading slash, which the bot translates into a slash command when AI parsing is enabled.
- **Entry_Date**: the date and time of a History_Entry written as day, short Indonesian month name and 24-hour time, such as 3 Jan 14:05, with the year added when it is not the current year.
- **Wallet_Not_Found_Reply**: the existing reply that names the unknown Wallet and lists the Registered_Chat's Wallets.

## Requirements

### Requirement 1: List recent transactions

**User Story:** As a user of a Registered_Chat, I want to see my latest transactions in the chat, so that I can check what was recorded without opening the spreadsheet.

#### Acceptance Criteria

1. WHEN a Registered_Chat sends History_Command without arguments, THE History_List SHALL show the most recent Active_Transaction first, up to Default_Count History_Entry lines in total.
2. THE History_Entry SHALL show the Entry_Date, the type as Pemasukan, Pengeluaran or Transfer, the amount in rupiah, the Wallet, the category and the description.
3. THE History_List SHALL format every amount with a dot as the thousands separator, matching the amounts in balance and recap replies.
4. WHEN an Active_Transaction has the same date and time as another, THE History_List SHALL place the one recorded later in the Ledger first.
5. IF a Chat that is not a Registered_Chat sends History_Command, THEN THE bot SHALL reply with the existing registration instructions.
6. THE History_Entry SHALL show the full description as recorded, without shortening it.
7. THE bot SHALL treat both names of History_Command as one command, with identical arguments and replies.

### Requirement 2: Choose how many entries to show

**User Story:** As a user, I want to ask for more or fewer entries, so that I can look further back or keep the reply short.

#### Acceptance Criteria

1. WHEN History_Command includes a whole number from 1 to Max_Count, THE History_List SHALL show at most that many History_Entry lines.
2. IF History_Command includes a number greater than Max_Count, THEN THE History_List SHALL show at most Max_Count History_Entry lines and state that the count was capped.
3. IF History_Command includes zero, a negative number or a non-whole number as the count, THEN THE bot SHALL reply with the History_Command format and an example, and show no History_List.
4. IF fewer matching Active_Transaction records exist than the requested count, THEN THE History_List SHALL show every matching one without an error.
5. WHEN more matching History_Entry lines exist than are shown, THE History_List SHALL state how many more exist.

### Requirement 3: Filter by wallet

**User Story:** As a user with several wallets, I want the history of one wallet, so that I can reconcile it against my bank or e-wallet app.

#### Acceptance Criteria

1. WHEN History_Command names a Wallet, THE History_List SHALL include only each Active_Transaction on that Wallet, including each Transfer into or out of it.
2. THE History_Command SHALL match the Wallet name case-insensitively, accept names of several words, and show the Wallet's name as registered.
3. WHEN History_Command gives both a count and a Wallet, THE History_List SHALL apply the count after filtering by that Wallet.
4. IF History_Command names a Wallet that the Registered_Chat does not have, THEN THE bot SHALL send the Wallet_Not_Found_Reply and no History_List.
5. WHEN History_Command names a Wallet, THE History_List header SHALL name that Wallet.
6. IF the whole argument text of History_Command matches a Wallet name exactly, THEN THE History_Command SHALL treat it as that Wallet, even when it is a number such as 007.

### Requirement 4: Show each transfer once

**User Story:** As a user, I want a transfer to appear as one movement, so that the list matches what I did rather than how it is stored.

#### Acceptance Criteria

1. THE History_List SHALL show each Transfer as one History_Entry naming the source Wallet, the destination Wallet, the amount and the description.
2. THE History_List SHALL count a Transfer as one History_Entry toward the requested count.
3. WHILE the History_List is filtered by a Wallet, THE History_Entry for a Transfer SHALL show whether money went into or out of that Wallet.
4. IF only one side of a Transfer is an Active_Transaction, THEN THE History_List SHALL show that side as a single History_Entry and not fail.

### Requirement 5: Data availability and empty states

**User Story:** As a user, I want history to reflect exactly what still counts in my balances, so that the list and my balances never disagree.

#### Acceptance Criteria

1. THE History_List SHALL exclude every Cancelled_Transaction.
2. THE History_List SHALL include each Active_Transaction recorded from any Chat linked to the same spreadsheet.
3. IF the Ledger has no Active_Transaction, THEN THE bot SHALL reply that there are no transactions yet and suggest how to record one.
4. IF a Wallet filter leaves no History_Entry, THEN THE bot SHALL reply that the named Wallet has no transactions yet.
5. IF the spreadsheet cannot be read, THEN THE bot SHALL reply with a short failure message in Indonesian and change nothing in the Ledger.
6. THE History_Command SHALL NOT add, change or cancel any transaction or Wallet.

### Requirement 6: Undo awareness

**User Story:** As a user about to undo, I want to know which entry Undo_Command will cancel, so that I do not cancel the wrong transaction.

#### Acceptance Criteria

1. WHEN the History_Entry that Undo_Command would cancel appears in the History_List, THE History_List SHALL mark that History_Entry as the Undo_Command target.
2. IF the History_Entry that Undo_Command would cancel is filtered out of the History_List, THEN THE History_List SHALL NOT mark any History_Entry as the Undo_Command target.
3. WHEN the latest Active_Transaction is one side of a Transfer, THE History_List SHALL mark the whole Transfer History_Entry as the Undo_Command target.

### Requirement 7: Language, help and natural language

**User Story:** As an Indonesian user on WhatsApp or Telegram, I want history to look and behave like the bot's other replies, so that it feels like part of the same bot.

#### Acceptance Criteria

1. THE History_List SHALL use Indonesian text, emoji prefixes and the bold markup used by the bot's other replies.
2. THE Help_Message SHALL list History_Command, its alias, its optional count, Period and Wallet, and one example.
3. THE History_List SHALL be delivered on every chat platform the bot serves, with the same content on each.
4. WHERE AI parsing is enabled, WHEN Natural_Language_Input asks to see recent transactions, THE bot SHALL run History_Command with any count, Period or Wallet the message names.
5. IF Natural_Language_Input asking for history names a Wallet the Registered_Chat does not have, THEN THE bot SHALL send the Wallet_Not_Found_Reply rather than an unfiltered History_List.
6. IF a History_List is longer than one message on a chat platform allows, THEN THE bot SHALL send it as several messages in order, never splitting a History_Entry.

### Requirement 8: Filter by period

**User Story:** As a user reviewing a week or a month, I want to list the transactions of that Period, so that I can see the individual entries behind a recap total.

#### Acceptance Criteria

1. WHEN History_Command names a Period, THE History_List SHALL include only each Active_Transaction dated within that Period, newest first.
2. THE History_Command SHALL accept the same Period words and the same week and month boundaries as the recap command.
3. WHEN History_Command names a Period, THE History_List header SHALL name the Period with its dates.
4. WHEN History_Command names a Period and a Wallet, THE History_List SHALL include only each Active_Transaction matching both.
5. IF a Period filter leaves no History_Entry, THEN THE bot SHALL reply that the Period has no transactions.
6. WHILE the History_List is filtered by a Period, THE History_List SHALL still respect the requested count, Default_Count and Max_Count.

