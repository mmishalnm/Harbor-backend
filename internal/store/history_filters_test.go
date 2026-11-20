package store

import (
	"bytes"
	"go.mongodb.org/mongo-driver/bson"
	"testing"
	"time"
)

func TestAdjustmentAndLocationFilterValidation(t *testing.T) {
	for _, delta := range []int64{-1000001, 0, 1000001} {
		if validateAdjustment(AdjustmentInput{QuantityDelta: delta, Reason: "Counted"}) == nil {
			t.Fatal(delta)
		}
	}
	if validateAdjustment(AdjustmentInput{QuantityDelta: 1, Reason: "   "}) == nil {
		t.Fatal("blank reason")
	}
	for _, delta := range []int64{-1000000, 1000000} {
		if err := validateAdjustment(AdjustmentInput{QuantityDelta: delta, Reason: "Counted"}); err != nil {
			t.Fatal(err)
		}
	}
	filter := bson.M{"locationId": "LOC-2"}
	constrainLocationFilter(filter, []string{"LOC-1", "LOC-2"})
	if filter["locationId"] != "LOC-2" {
		t.Fatal(filter)
	}
	constrainLocationFilter(filter, []string{"LOC-1"})
	if len(filter["locationId"].(bson.M)["$in"].([]string)) != 0 {
		t.Fatal(filter)
	}
}
func TestHistoryOrderingFiltersAndIndianDateBoundaries(t *testing.T) {
	at := time.Date(2026, 11, 18, 18, 30, 0, 0, time.UTC)
	entries := []LedgerEntry{{EntryID: "PAY-1", Type: "payment", Method: "cash", CreatedAt: at}, {EntryID: "PAY-2", Type: "payment", Method: "cash", CreatedAt: at}, {EntryID: "INV-1", Type: "invoice", CreatedAt: at.Add(time.Hour)}, {EntryID: "PAY-3", Type: "payment", Method: "upi", CreatedAt: at.Add(24 * time.Hour)}}
	f, err := parseHistoryFilters(map[string]string{"type": "payment", "method": "cash", "dateFrom": "2026-11-19", "dateTo": "2026-11-19"}, false)
	if err != nil {
		t.Fatal(err)
	}
	got := filteredLedgerEntries(entries, f)
	if len(got) != 2 || got[0].EntryID != "PAY-2" || got[1].EntryID != "PAY-1" || entries[0].EntryID != "PAY-1" {
		t.Fatal(got)
	}
	all := filteredLedgerEntries(entries, historyFilters{})
	if all[0].EntryID != "PAY-3" || all[1].EntryID != "INV-1" {
		t.Fatal(all)
	}
	for _, filters := range []map[string]string{{"type": "delete"}, {"method": "bitcoin"}, {"dateFrom": "2026-02-30"}, {"dateFrom": "2026-11-20", "dateTo": "2026-11-19"}} {
		if _, err := parseHistoryFilters(filters, false); err == nil {
			t.Fatal(filters)
		}
	}
	partner, err := parseHistoryFilters(map[string]string{"type": "salaryAdjustment"}, true)
	if err != nil {
		t.Fatal(err)
	}
	history := filteredCashHistory([]PartnerCashHistory{{Kind: "collection", CreatedAt: at}, {Kind: "salaryAdjustment", CreatedAt: at}}, partner)
	if len(history) != 1 || history[0].Kind != "salaryAdjustment" {
		t.Fatal(history)
	}
}
func TestLedgerPDFMultipageAndSafeFilename(t *testing.T) {
	l := Ledger{BusinessName: "Arun Frozen Foods", Customer: Customer{Name: "Sunrise Stores", Address: "Kahakootam"}, GeneratedAt: time.Now(), Balance: -1015, InvoiceTotal: 2485, PaymentTotal: 3500, AdvanceCredit: 1015}
	for i := 0; i < 90; i++ {
		l.Entries = append(l.Entries, LedgerEntry{EntryID: "PAY-000001", Type: "payment", Method: "cash", Amount: -35, CreatedAt: l.GeneratedAt})
	}
	pdf, err := renderLedgerPDF(l, map[string]string{"type": "payment"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || bytes.Count(pdf, []byte("/Type /Page")) < 3 {
		t.Fatal("invalid/missing PDF pages", len(pdf))
	}
	for name, want := range map[string]string{"../../Sunrise Stores\r\n": "Sunrise-Stores-ledger.pdf", "/": "Customer-ledger.pdf"} {
		if got := ledgerFileName(name); got != want {
			t.Fatal(got, want)
		}
	}
}
