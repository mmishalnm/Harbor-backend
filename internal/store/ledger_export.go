package store

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"strings"
	"unicode"

	"github.com/signintech/gopdf"
)

// Fonts are embedded so a standalone go build needs no assets at runtime.
//
//go:embed assets/Roboto-Regular.ttf
var ledgerFont []byte

//go:embed assets/Roboto-Bold.ttf
var ledgerBoldFont []byte

type LedgerDocument struct {
	FileName string `json:"fileName"`
	MIMEType string `json:"mimeType"`
	Data     []byte `json:"data"`
}

func (s *Store) ExportCustomerLedger(ctx context.Context, a Actor, customerID, tripID string, filters map[string]string) (LedgerDocument, error) {
	ledger, err := s.customerLedger(ctx, a, customerID, tripID, Page{Page: 1, Limit: 200}, true, filters)
	if err != nil {
		return LedgerDocument{}, err
	}
	data, err := renderLedgerPDF(ledger, filters)
	if err != nil {
		return LedgerDocument{}, err
	}
	return LedgerDocument{FileName: ledgerFileName(ledger.Customer.Name), MIMEType: "application/pdf", Data: data}, nil
}
func ledgerFileName(name string) string {
	var out strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			out.WriteRune(r)
		} else if out.Len() > 0 && !strings.HasSuffix(out.String(), "-") {
			out.WriteByte('-')
		}
		if out.Len() >= 60 {
			break
		}
	}
	label := strings.Trim(out.String(), "-")
	if label == "" {
		label = "Customer"
	}
	return label + "-ledger.pdf"
}
func pdfText(value string) string {
	return strings.Join(strings.FieldsFunc(value, unicode.IsControl), " ")
}

// renderLedgerPDF receives one complete transaction snapshot, not independently
// fetched pages. Filtered history never changes the all-time customer balance.
func renderLedgerPDF(l Ledger, filters map[string]string) ([]byte, error) {
	var pdf gopdf.GoPdf
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := pdf.AddTTFFontByReader("regular", bytes.NewReader(ledgerFont)); err != nil {
		return nil, err
	}
	if err := pdf.AddTTFFontByReader("bold", bytes.NewReader(ledgerBoldFont)); err != nil {
		return nil, err
	}
	var drawErr error
	text := func(x, y, w, size float64, value string, bold bool) {
		if drawErr != nil {
			return
		}
		font := "regular"
		if bold {
			font = "bold"
		}
		if drawErr = pdf.SetFont(font, "", size); drawErr != nil {
			return
		}
		value = pdfText(value)
		// Keep each table cell inside its column, including very long customer names.
		for {
			width, err := pdf.MeasureTextWidth(value)
			if err != nil {
				drawErr = err
				return
			}
			if width <= w || len([]rune(value)) <= 1 {
				break
			}
			r := []rune(strings.TrimSuffix(value, "…"))
			value = string(r[:len(r)-1]) + "…"
		}
		pdf.SetXY(x, y)
		drawErr = pdf.Cell(&gopdf.Rect{W: w, H: size + 5}, value)
	}
	page := 0
	footer := func() {
		pdf.SetTextColor(111, 124, 145)
		text(36, 803, 420, 9, "Harbor by Code Wayne · Statement of account · INR", false)
		text(510, 803, 50, 9, fmt.Sprintf("Page %d", page), false)
	}
	header := func(first bool) float64 {
		pdf.AddPage()
		page++
		pdf.SetTextColor(91, 67, 241)
		text(36, 30, 400, 19, "HARBOR", true)
		pdf.SetTextColor(21, 37, 60)
		text(36, 61, 520, 17, l.BusinessName, true)
		text(36, 90, 520, 12, "Customer ledger · "+l.Customer.Name, true)
		if !first {
			return 125
		}
		pdf.SetTextColor(111, 124, 145)
		text(36, 113, 520, 10, l.Customer.Phone+"   "+l.Customer.Address, false)
		text(36, 132, 520, 9, "Generated "+l.GeneratedAt.In(tripTimezone).Format("02 Jan 2006, 03:04 PM")+" IST · Newest transactions first", false)
		pdf.SetFillColor(240, 236, 255)
		pdf.RectFromUpperLeftWithStyle(36, 159, 523, 87, "F")
		pdf.SetTextColor(91, 67, 241)
		text(50, 170, 490, 10, "ALL-TIME LEDGER BALANCE", true)
		label := "Amount due"
		amount := l.Balance
		if amount < 0 {
			label = "Advance available"
			amount = -amount
		}
		if amount == 0 {
			label = "All settled"
		}
		text(50, 192, 490, 23, fmt.Sprintf("₹ %.2f", amount), true)
		text(50, 221, 490, 10, label, false)
		pdf.SetTextColor(21, 37, 60)
		text(36, 263, 255, 10, fmt.Sprintf("Invoiced: ₹ %.2f", l.InvoiceTotal), false)
		text(304, 263, 255, 10, fmt.Sprintf("Paid: ₹ %.2f", l.PaymentTotal), false)
		text(36, 284, 255, 10, fmt.Sprintf("Outstanding: ₹ %.2f", l.Outstanding), false)
		text(304, 284, 255, 10, fmt.Sprintf("Advance: ₹ %.2f", l.AdvanceCredit), false)
		pdf.SetTextColor(111, 124, 145)
		summary := "All transactions · Balances above include all history."
		if len(filters) > 0 {
			summary = "Filtered transactions · Balances above include all history."
		}
		text(36, 315, 523, 10, summary, false)
		parts := []string{}
		for _, key := range []string{"type", "method", "dateFrom", "dateTo"} {
			if v := filters[key]; v != "" {
				parts = append(parts, key+": "+v)
			}
		}
		if len(parts) > 0 {
			text(36, 333, 523, 9, strings.Join(parts, " · "), false)
		}
		return 361
	}
	tableHeader := func(y float64) {
		pdf.SetFillColor(246, 247, 251)
		pdf.RectFromUpperLeftWithStyle(36, y, 523, 25, "F")
		pdf.SetTextColor(111, 124, 145)
		for _, v := range []struct {
			x, w  float64
			label string
		}{{43, 98, "Date (IST)"}, {143, 113, "Type / method"}, {260, 132, "Reference"}, {399, 74, "Debit (₹)"}, {481, 74, "Credit (₹)"}} {
			text(v.x, y+5, v.w, 9, v.label, true)
		}
	}
	y := header(true)
	tableHeader(y)
	y += 30
	for _, e := range l.Entries {
		if y+33 > 780 {
			footer()
			y = header(false)
			tableHeader(y)
			y += 30
		}
		pdf.SetTextColor(21, 37, 60)
		kind := "Invoice"
		if e.Type == "payment" {
			kind = "Payment / " + e.Method
		}
		if e.Type == "cancelledInvoice" {
			kind = "Cancelled invoice"
		}
		reference := e.InvoiceNo
		if reference == "" {
			reference = e.EntryID
		}
		text(43, y, 98, 9, e.CreatedAt.In(tripTimezone).Format("02 Jan 06 15:04"), false)
		text(143, y, 113, 9, kind, false)
		text(260, y, 132, 9, reference, false)
		if e.Type == "payment" {
			pdf.SetTextColor(7, 139, 117)
			text(481, y, 74, 10, fmt.Sprintf("%.2f", -e.Amount), true)
		} else {
			text(399, y, 74, 10, fmt.Sprintf("%.2f", e.Amount), true)
		}
		pdf.SetStrokeColor(231, 234, 242)
		pdf.SetLineWidth(.5)
		pdf.Line(36, y+25, 559, y+25)
		y += 33
	}
	if len(l.Entries) == 0 {
		text(43, y+10, 500, 11, "No transactions match these filters.", false)
	}
	footer()
	if drawErr != nil {
		return nil, drawErr
	}
	return pdf.GetBytesPdfReturnErr()
}
