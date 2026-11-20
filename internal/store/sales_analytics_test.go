package store

import (
	"testing"
	"time"
)

func salesFixture(t *testing.T, filters map[string]string) (*salesAccumulator, time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, tripTimezone)
	f, err := parseSalesFilters(filters, now)
	if err != nil {
		t.Fatal(err)
	}
	return newSalesAccumulator(f, map[string]Variant{"MVN-1": {VariantID: "MVN-1", ProductID: "MPN-1", Name: "5kg", Price: 999}, "MVN-2": {VariantID: "MVN-2", ProductID: "MPN-2", Name: "A4", Price: 999}}, map[string]Product{"MPN-1": {ProductID: "MPN-1", Name: "ICE"}, "MPN-2": {ProductID: "MPN-2", Name: "Notebook"}}), now
}
func TestSalesUsesSavedAmountsDistinctInvoicesAndCents(t *testing.T) {
	a, now := salesFixture(t, map[string]string{"dateFrom": "2026-11-19", "dateTo": "2026-11-20"})
	a.add(Invoice{InvoiceID: "INV-1", CustomerID: "CUS-1", Status: "issued", Base: Base{CreatedAt: now}, Lines: []InvoiceLine{{VariantID: "MVN-1", Quantity: 2, UnitPrice: 120, Amount: 240}, {VariantID: "MVN-2", Quantity: 1, UnitPrice: 65.05, Amount: 65.05}}})
	a.add(Invoice{InvoiceID: "INV-2", CustomerID: "CUS-1", Status: "issued", Base: Base{CreatedAt: now}, Lines: []InvoiceLine{{VariantID: "MVN-1", Quantity: 1, UnitPrice: 120.05, Amount: 120.05}}})
	a.add(Invoice{Status: "cancelled", Base: Base{CreatedAt: now}, Lines: []InvoiceLine{{VariantID: "MVN-1", Quantity: 100, Amount: 10000}}})
	result := a.result(Page{1, 1}, Page{1, 1}, now)
	if result.Summary.SalesAmount != 425.10 || result.Summary.InvoiceCount != 2 || result.Summary.CustomerCount != 1 || result.Summary.UnitsSold != 4 || result.Summary.AverageInvoiceValue != 212.55 {
		t.Fatal(result.Summary)
	}
	if result.Products.Total != 2 || len(result.Products.Items) != 1 || result.Products.Items[0].SalesAmount != 360.05 || result.Products.Items[0].AverageUnitPrice != 120.02 {
		t.Fatal(result.Products)
	}
	if result.Trend.Total != 2 || result.Trend.Items[0].SalesAmount != 0 {
		t.Fatal(result.Trend)
	}
	last := a.result(Page{2, 1}, Page{2, 1}, now)
	if last.Summary != result.Summary || last.Trend.Items[0].SalesAmount != 425.10 || last.Products.Items[0].SalesAmount != 65.05 {
		t.Fatal(last)
	}
}
func TestSalesProductFilteringAndISTBoundary(t *testing.T) {
	a, now := salesFixture(t, map[string]string{"dateFrom": "2026-11-20", "dateTo": "2026-11-20", "productId": "MPN-1", "customerId": "CUS-1"})
	at := time.Date(2026, 11, 19, 18, 30, 0, 0, time.UTC)
	for _, c := range []struct {
		time     time.Time
		customer string
	}{{at.Add(-time.Nanosecond), "CUS-1"}, {at, "CUS-1"}, {at.Add(24 * time.Hour), "CUS-1"}, {at, "CUS-2"}} {
		a.add(Invoice{Status: "issued", CustomerID: c.customer, Base: Base{CreatedAt: c.time}, Lines: []InvoiceLine{{VariantID: "MVN-1", Quantity: 1, Amount: 120}, {VariantID: "MVN-2", Quantity: 1, Amount: 65}}})
	}
	result := a.result(Page{1, 20}, Page{1, 31}, now)
	if result.Summary.SalesAmount != 120 || result.Summary.InvoiceCount != 1 || result.Summary.UnitsSold != 1 || result.Collections != nil || result.Trend.Items[0].Date != "2026-11-20" {
		t.Fatal(result)
	}
}
func TestSalesFilterValidationDefaultsAndMonthlyZeroBuckets(t *testing.T) {
	now := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	f, err := parseSalesFilters(map[string]string{}, now)
	if err != nil || f.period.DateFrom != "2026-11-01" || f.period.DateTo != "2026-11-20" {
		t.Fatal(f, err)
	}
	for _, v := range []map[string]string{{"dateFrom": "2026-02-30"}, {"dateFrom": "2026-11-21", "dateTo": "2026-11-20"}, {"dateFrom": "2024-01-01", "dateTo": "2026-01-01"}, {"productId": "CUS-1"}, {"variantId": "INVALID"}, {"groupBy": "year"}} {
		if _, err := parseSalesFilters(v, now); err == nil {
			t.Fatal(v)
		}
	}
	a, at := salesFixture(t, map[string]string{"dateFrom": "2026-09-15", "dateTo": "2026-11-20", "groupBy": "month"})
	result := a.result(Page{1, 20}, Page{1, 31}, at)
	if result.Trend.Total != 3 || result.Trend.Items[0].Date != "2026-09-01" || result.Trend.Items[2].Date != "2026-11-01" || result.Summary.SalesAmount != 0 || result.Summary.AverageInvoiceValue != 0 {
		t.Fatal(result)
	}
}
