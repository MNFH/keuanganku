# Requirements review — transaction-history (round 1, full)

Reviewers: scope, completeness, clarity, ears. Duplicates merged.

## Decide
- [med] D1 R6.1–R6.3: an unfiltered list already shows the undo target first. The mark adds three criteria and logic to work out the target separately. → keep, or defer to Non-goals until users undo the wrong entry (scope)
- [med] D2 R7.4–R7.5: natural-language history is not in the Introduction's promise. It needs a new AI intent action plus its not-found rule. → keep, or defer to Non-goals until free-form requests for history appear (scope)
- [med] D3 R7.6: splitting a long reply across messages is new delivery behaviour. It is needed only because descriptions are never shortened (R1.6, the user's decision) and up to 30 entries are allowed. → keep the split, or drop R7.6 and accept that a long list may fail on Telegram (scope)
- [med] D4 R4.4: the bot always cancels both sides of a Transfer together, so a half-cancelled Transfer only arises from a hand-edited sheet. → cut, or keep and define the form (scope; clarity low fix on its wording)
- [med] D5 R5.3/R5.4/R8.5: three empty-state replies overlap when the ledger is empty or when filters combine, and R5.4 is false when the Wallet has entries outside the Period. → merge into one "no transactions" reply that names any filter in force, or keep three with an order of precedence (scope decide; clarity, completeness and ears high fix)
- [low] D6 R3.3/R8.4/R8.6: each restates "filters combine, count applies after filtering". → merge into R2.1 (scope)
- [low] D7 R1.3/R1.7/R5.6/R7.3: these repeat other text: the amount format (R7.1), the alias (glossary), read-only (R5.5 and the Introduction) and platform delivery. → cut (scope)
- [med] D8 Argument parsing: nothing says what happens with a wrong order or trailing text (`/riwayat BCA 5`, `/riwayat bulan ini 10`), or when a leading number could start a Wallet name (`10 Ribu`). → (a) longest Wallet match wins and a wrong order gets the format reply; (b) a leading number is always the count unless the whole text is a Wallet; (c) refuse ambiguous input (clarity, completeness)
- [med] D9 Period set: the glossary lists six relative periods but calls them "the same words the recap command accepts", and the recap command also accepts named months and dates. → (a) every period the recap command accepts; (b) only the six relative ones; (c) six now, named ones later (clarity, completeness, ears)

## Fix
- [high] F1 R1.2 vs R4.1: R1.2 gives every entry one Wallet and a category, which contradicts R4.1 for a Transfer. → R1.2 applies to income and expense; R4.1 lists Entry_Date, type Transfer, source, destination, amount and description (clarity, completeness, ears)
- [high] F2 R5.3/R5.4/R8.5 conflicting empty replies → settled by D5
- [med] F3 "History_Entry lines" in Default_Count, Max_Count, R1.1, R2.1, R2.2 and R2.5 is ambiguous for multi-line entries. → count History_Entry items (clarity, ears)
- [med] F4 "latest" (Undo_Command) vs "most recent" (R1.1) are not defined against the same order. → the list is ordered by Entry_Date with ties broken by Ledger order; Undo_Command cancels the transaction recorded last (clarity, completeness)
- [med] F5 Period glossary vs R8.2 contradict → settled by D9
- [low] F6 Entry_Date "current year" does not name the clock → the bot's local date at reply time (clarity)
- [low] F7 The Introduction's "the chat's" transactions contradicts R5.2 (shared ledger). → "the chat's Ledger" (completeness)
- [low] F8 R5.5 "short" is untestable. → "that the history could not be loaded" (ears)

## Notes for the next stage
- R7.6 (if kept): splitting needs a per-platform message-length limit. It must stay out of the handler, or be expressed as several replies.
- R6 (if kept): marking the undo target means working out the same target `/batal` would cancel, from the same read.

# Round 2

Reviewers: completeness, clarity, ears (verify). Duplicates merged.

## Decide
- [med] D10 R7.5 vs R1.5: one entry longer than a platform message can be neither split nor shortened. → (a) split only that entry; (b) shorten only that entry with a note; (c) omit it with a note (completeness, clarity; ears as fix)
- [med] D11 R2.1 vs Period: under count-first, `/riwayat 5 januari` reads as 5 entries from January rather than entries on 5 January. → (a) a leading number is always the count, so a date needs a count before it; (b) a number that starts a valid Period is that Period, otherwise the count (clarity picks b, ears picks a)
- [med] D12 R8.3 vs R3.3: an invalid date such as `/riwayat 31 feb` could get the Format_Reply or the Wallet_Not_Found_Reply. → (a) date-shaped text the recap command rejects gets the Format_Reply; (b) delete R8.3 and treat it as a Wallet name; (c) Format_Reply whenever text is neither a Period nor a Wallet (ears)
- [med] D13 R5.5/R5.6 vs existing housekeeping: every command first converts legacy typed balances and recreates deleted tabs, and both write to the sheet. → (a) history skips both; (b) allow both and narrow R5.6 to "no transaction or Wallet is added or cancelled, and no displayed balance changes"; (c) refuse on legacy sheets (clarity)

## Fix
- [high] F9 R3.3 vs R3.6: `/riwayat 5 Dana` with no Dana Wallet triggers both replies. → R3.6 covers only text after a matched Wallet and a wrong order; unmatched trailing text is a Wallet name for R3.3 (completeness, ears)
- [med] F10 Entry_Date is the minute-precision display but is used for sorting (R1.3) and Period bounds (R8.1). → separate Recorded_Time (to the second) from its display (clarity, ears)
- [low] F11 The Introduction reads as one filter at a time. → "or any combination of these" (completeness)
