// Package report renders a monthly financial recap as a PDF, with a bar
// chart of top expense categories plus category and wallet-balance tables.
package report

import (
	"bytes"
	"fmt"
	"time"

	"github.com/jung-kurt/gofpdf"
	"github.com/nurfaizh/keuanganku/internal/model"
	"github.com/nurfaizh/keuanganku/internal/recap"
	chart "github.com/wcharczuk/go-chart/v2"
)

const maxChartBars = 6

// GenerateMonthly builds a PDF report for the given title and transaction
// set, along with a snapshot of current wallet balances, and returns the
// raw PDF bytes.
func GenerateMonthly(title string, txs []model.Transaction, wallets []model.Wallet) ([]byte, error) {
	stats := recap.Compute(txs)

	pdf := gofpdf.New("P", "mm", "A4", "")
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetTitle(tr(title), false)
	pdf.AddPage()

	pageW, _ := pdf.GetPageSize()
	margin := 15.0
	pdf.SetMargins(margin, margin, margin)
	contentW := pageW - 2*margin

	pdf.SetFont("Helvetica", "B", 18)
	pdf.CellFormat(contentW, 10, tr(title), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(120, 120, 120)
	pdf.CellFormat(contentW, 6, tr("Dibuat: "+time.Now().Format("2 Jan 2006 15:04")), "", 1, "L", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
	pdf.Ln(4)

	drawSummary(pdf, tr, contentW, stats)
	pdf.Ln(8)

	if len(stats.ExpenseByCategory) > 0 {
		if imgBytes, err := renderExpenseChart(stats.ExpenseByCategory); err == nil {
			pdf.SetFont("Helvetica", "B", 12)
			pdf.CellFormat(contentW, 8, tr("Pengeluaran per Kategori"), "", 1, "L", false, 0, "")
			opt := gofpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}
			pdf.RegisterImageOptionsReader("expense-chart", opt, bytes.NewReader(imgBytes))
			imgH := contentW * (500.0 / 700.0)
			pdf.ImageOptions("expense-chart", margin, pdf.GetY(), contentW, imgH, false, opt, 0, "")
			pdf.SetY(pdf.GetY() + imgH + 6)
		}
	}

	drawCategoryTable(pdf, tr, contentW, "Rincian Pengeluaran", stats.ExpenseByCategory, stats.TotalExpense)
	pdf.Ln(6)
	drawCategoryTable(pdf, tr, contentW, "Rincian Pemasukan", stats.IncomeByCategory, stats.TotalIncome)
	pdf.Ln(6)
	drawWalletTable(pdf, tr, contentW, wallets)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("render pdf: %w", err)
	}
	return buf.Bytes(), nil
}

func drawSummary(pdf *gofpdf.Fpdf, tr func(string) string, w float64, stats recap.Stats) {
	net := stats.TotalIncome - stats.TotalExpense
	colW := w / 3

	pdf.SetFont("Helvetica", "B", 11)
	pdf.SetTextColor(20, 130, 20)
	pdf.CellFormat(colW, 8, tr("Pemasukan"), "1", 0, "C", false, 0, "")
	pdf.SetTextColor(180, 30, 30)
	pdf.CellFormat(colW, 8, tr("Pengeluaran"), "1", 0, "C", false, 0, "")
	pdf.SetTextColor(30, 30, 30)
	pdf.CellFormat(colW, 8, tr("Selisih"), "1", 1, "C", false, 0, "")

	pdf.SetFont("Helvetica", "", 12)
	pdf.SetTextColor(20, 130, 20)
	pdf.CellFormat(colW, 10, tr("Rp "+model.FormatAmount(stats.TotalIncome)), "1", 0, "C", false, 0, "")
	pdf.SetTextColor(180, 30, 30)
	pdf.CellFormat(colW, 10, tr("Rp "+model.FormatAmount(stats.TotalExpense)), "1", 0, "C", false, 0, "")
	if net >= 0 {
		pdf.SetTextColor(20, 130, 20)
	} else {
		pdf.SetTextColor(180, 30, 30)
	}
	pdf.CellFormat(colW, 10, tr("Rp "+model.FormatAmount(net)), "1", 1, "C", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
}

func drawCategoryTable(pdf *gofpdf.Fpdf, tr func(string) string, w float64, heading string, entries []recap.CategoryTotal, total float64) {
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(w, 8, tr(heading), "", 1, "L", false, 0, "")

	if len(entries) == 0 {
		pdf.SetFont("Helvetica", "I", 10)
		pdf.CellFormat(w, 6, tr("Tidak ada data."), "", 1, "L", false, 0, "")
		return
	}

	colCat, colAmt, colPct := w*0.5, w*0.3, w*0.2

	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetFillColor(230, 230, 230)
	pdf.CellFormat(colCat, 7, tr("Kategori"), "1", 0, "L", true, 0, "")
	pdf.CellFormat(colAmt, 7, tr("Jumlah"), "1", 0, "R", true, 0, "")
	pdf.CellFormat(colPct, 7, "%", "1", 1, "R", true, 0, "")

	pdf.SetFont("Helvetica", "", 10)
	for _, e := range entries {
		pct := 0.0
		if total > 0 {
			pct = e.Total / total * 100
		}
		pdf.CellFormat(colCat, 7, tr(e.Category), "1", 0, "L", false, 0, "")
		pdf.CellFormat(colAmt, 7, tr("Rp "+model.FormatAmount(e.Total)), "1", 0, "R", false, 0, "")
		pdf.CellFormat(colPct, 7, fmt.Sprintf("%.1f%%", pct), "1", 1, "R", false, 0, "")
	}
}

func drawWalletTable(pdf *gofpdf.Fpdf, tr func(string) string, w float64, wallets []model.Wallet) {
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(w, 8, tr("Saldo Dompet Saat Ini"), "", 1, "L", false, 0, "")

	if len(wallets) == 0 {
		pdf.SetFont("Helvetica", "I", 10)
		pdf.CellFormat(w, 6, tr("Belum ada dompet."), "", 1, "L", false, 0, "")
		return
	}

	colName, colBal := w*0.6, w*0.4

	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetFillColor(230, 230, 230)
	pdf.CellFormat(colName, 7, tr("Dompet"), "1", 0, "L", true, 0, "")
	pdf.CellFormat(colBal, 7, tr("Saldo"), "1", 1, "R", true, 0, "")

	pdf.SetFont("Helvetica", "", 10)
	var total float64
	for _, wlt := range wallets {
		pdf.CellFormat(colName, 7, tr(wlt.Name), "1", 0, "L", false, 0, "")
		pdf.CellFormat(colBal, 7, tr("Rp "+model.FormatAmount(wlt.Balance)), "1", 1, "R", false, 0, "")
		total += wlt.Balance
	}
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(colName, 7, tr("Total"), "1", 0, "L", false, 0, "")
	pdf.CellFormat(colBal, 7, tr("Rp "+model.FormatAmount(total)), "1", 1, "R", false, 0, "")
}

// minLabeledSlicePct hides the on-slice text label for any wedge smaller
// than this share of the total, to keep labels from overlapping — its exact
// amount is still in the table below the chart.
const minLabeledSlicePct = 4.0

func renderExpenseChart(categories []recap.CategoryTotal) ([]byte, error) {
	top := categories
	var otherTotal float64
	if len(top) > maxChartBars {
		for _, c := range categories[maxChartBars:] {
			otherTotal += c.Total
		}
		top = categories[:maxChartBars]
	}

	grandTotal := otherTotal
	for _, c := range top {
		grandTotal += c.Total
	}
	if grandTotal == 0 {
		grandTotal = 1
	}

	values := make([]chart.Value, 0, len(top)+1)
	for _, c := range top {
		pct := c.Total / grandTotal * 100
		values = append(values, chart.Value{
			Value: c.Total,
			Label: sliceLabel(truncateLabel(c.Category, 14), pct),
		})
	}
	if otherTotal > 0 {
		pct := otherTotal / grandTotal * 100
		// "Lain-lain" (not "Lainnya") — this bucket is distinct from any
		// user category actually named "Lainnya" (the app's own default
		// category name), so the two don't collide in the chart.
		values = append(values, chart.Value{
			Value: otherTotal,
			Label: sliceLabel("Lain-lain", pct),
		})
	}

	pc := chart.PieChart{
		// go-chart places each slice's label at a fixed 2/3-radius offset
		// with no collision detection, so a bigger canvas (same absolute
		// font size) is what actually buys more room between labels.
		Width:  1400,
		Height: 1000,
		Values: values,
	}

	buf := new(bytes.Buffer)
	if err := pc.Render(chart.PNG, buf); err != nil {
		return nil, fmt.Errorf("render chart: %w", err)
	}
	return buf.Bytes(), nil
}

// sliceLabel returns "" (no text drawn on the wedge) for slices too small to
// label without overlapping their neighbors.
func sliceLabel(name string, pct float64) string {
	if pct < minLabeledSlicePct {
		return ""
	}
	return fmt.Sprintf("%s (%.1f%%)", name, pct)
}

func truncateLabel(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
