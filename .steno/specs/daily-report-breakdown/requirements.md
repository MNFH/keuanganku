# Requirements Document

## Introduction

Today the PDF report, both the one a user requests with `/rekap ... pdf` and the scheduled weekly or monthly one, shows only period totals (income, expense, difference), a pie chart and tables of expenses and income per category, and current wallet balances. A user who wants to know on which days the money went, or which day of the month was the expensive one, has to open the spreadsheet and add it up by hand.

This feature adds a per-day breakdown to the PDF report: for each day of the reported period, the day's total income, total expense and difference. The text `/rekap` reply in chat stays as it is, so chat messages stay short.

## Glossary

- **PDF_Report**: the PDF document the bot sends for a period, on request (`/rekap ... pdf`) or as a scheduled weekly or monthly report.
- **Text_Recap**: the plain chat reply to `/rekap` without `pdf`.
- **Report_Period**: the date range a PDF_Report covers, such as a calendar month, a Monday-to-Sunday week or a single day, in the bot's local time.
- **Daily_Breakdown**: a section of the PDF_Report with one Day_Row per calendar day of the Report_Period, plus a total row.
- **Day_Row**: one line of the Daily_Breakdown showing a date, that day's Daily_Income, Daily_Expense and Daily_Difference.
- **Daily_Income**: the sum of active income transactions whose date falls on that calendar day.
- **Daily_Expense**: the sum of active expense transactions whose date falls on that calendar day.
- **Daily_Difference**: Daily_Income minus Daily_Expense for that day; negative when the day spent more than it earned.
- **Active_Transaction**: a recorded income or expense that the user has not cancelled (undo). Transfers never count as an Active_Transaction for reporting.
- **On_Demand_Request**: a user asking the bot for a PDF_Report in chat, as opposed to a scheduled one.
- **Period_Summary**: the existing block at the top of the PDF_Report showing total income, total expense and difference for the whole Report_Period.

## Requirements

### Requirement 1: Daily breakdown section in the PDF report

**User Story:** As a user reviewing my spending, I want the PDF report to show income and expense for each day, so that I can see which days I spent the most without opening the spreadsheet.

#### Acceptance Criteria

1. WHEN a PDF_Report is generated for a Report_Period longer than one day, THE PDF_Report SHALL include a Daily_Breakdown as its last section, after the wallet balances.
2. THE Daily_Breakdown SHALL order each Day_Row chronologically, earliest date first.
3. THE Daily_Breakdown SHALL end with a total row showing the summed income, expense and difference across every Day_Row.
4. IF the Report_Period is a single day, THEN THE PDF_Report SHALL omit the Daily_Breakdown, because the Period_Summary already shows that day.
5. IF a Daily_Breakdown does not fit on the current page, THEN THE PDF_Report SHALL continue it on the next page with its column headings repeated.
6. THE Daily_Breakdown SHALL present its figures as a table only, without any chart.

### Requirement 2: Every day of the period is shown

**User Story:** As a user, I want days with no transactions to appear too, so that I can tell a quiet day apart from a day I forgot to record.

#### Acceptance Criteria

1. THE Daily_Breakdown SHALL contain exactly one Day_Row for every calendar day in the Report_Period, including a day without any Active_Transaction.
2. WHEN a day has no Active_Transaction, THE Day_Row SHALL show zero for Daily_Income, Daily_Expense and Daily_Difference.
3. WHEN the Report_Period contains no Active_Transaction at all, THE Daily_Breakdown SHALL still list every day with zero amounts.
4. THE Daily_Breakdown SHALL NOT include any Day_Row for a date outside the Report_Period.

### Requirement 3: Daily figures agree with the rest of the report

**User Story:** As a user, I want the daily numbers to add up to the report totals, so that I can trust both.

#### Acceptance Criteria

1. THE Daily_Breakdown total row SHALL equal the Period_Summary's total income, total expense and difference exactly.
2. THE Daily_Breakdown SHALL count only an Active_Transaction, excluding cancelled entries and both legs of every transfer.
3. THE Daily_Breakdown SHALL assign each transaction to the calendar day of its recorded date in the bot's local time.
4. WHEN the user has cancelled a transaction before the PDF_Report is generated, THE Daily_Breakdown SHALL exclude it from its day.

### Requirement 4: Same breakdown for on-demand and scheduled reports

**User Story:** As a user who receives automatic reports, I want the scheduled report to include the daily breakdown too, so that every PDF I get looks the same.

#### Acceptance Criteria

1. WHEN a user makes an On_Demand_Request for a week, a month or a named month, THE PDF_Report SHALL include the Daily_Breakdown.
2. WHEN the bot sends a scheduled weekly or monthly PDF_Report, THE PDF_Report SHALL include the Daily_Breakdown.
3. THE Text_Recap SHALL remain exactly as it was before this feature.
4. THE Text_Recap SHALL NOT show any per-day figures.
5. THE PDF_Report SHALL keep the Period_Summary, category chart, category tables and wallet balances it showed before this feature.

### Requirement 5: Indonesian presentation and amount formatting

**User Story:** As an Indonesian user, I want the daily breakdown to read naturally, so that it matches the rest of the report.

#### Acceptance Criteria

1. THE Daily_Breakdown SHALL use Indonesian text for its heading, column headings and total row label.
2. THE Day_Row SHALL show its date as Indonesian day name, day of month and abbreviated Indonesian month, without the year, for example "Senin, 3 Nov".
3. THE Daily_Breakdown SHALL show every amount prefixed with "Rp" and using "." as the thousands separator, as elsewhere in the PDF_Report.
4. WHEN a Daily_Difference is negative, THE Day_Row SHALL show it in the same red colour the Period_Summary uses for a negative difference.
5. THE Daily_Breakdown SHALL NOT highlight any Day_Row for having the highest income or expense.
