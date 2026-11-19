package store

import (
	"fmt"
	"math"
	"strings"
	"time"

	"harbor/internal/apperr"
)

func RoundMoney(v float64) float64 { return math.Round(v*100) / 100 }
func cents(v float64) int64        { return int64(math.Round(v * 100)) }
func money(v int64) float64        { return float64(v) / 100 }
func validMoney(v float64, allowZero bool) error {
	if math.IsNaN(v) || math.IsInf(v, 0) || v > 1e9 || v < 0 || (!allowZero && RoundMoney(v) <= 0) {
		return apperr.Validation("Amount must be finite, positive, and at most 1,000,000,000")
	}
	return nil
}
func validName(v string) error {
	if strings.TrimSpace(v) == "" || len(v) > 200 {
		return apperr.Validation("Name is required and must be at most 200 bytes")
	}
	return nil
}
func validText(v string) error {
	if len(v) > 2000 {
		return apperr.Validation("Text must be at most 2000 bytes")
	}
	return nil
}
func validItems(items []Item, allowEmpty bool) error {
	if len(items) > 200 || (!allowEmpty && len(items) == 0) {
		return apperr.Validation("Provide between 1 and 200 items")
	}
	seen := map[string]bool{}
	for _, it := range items {
		if it.VariantID == "" || it.Quantity < 1 || it.Quantity > 1000000 {
			return apperr.Validation("Each item needs a variantId and quantity between 1 and 1,000,000")
		}
		if seen[it.VariantID] {
			return apperr.Validation("Duplicate variantId in items")
		}
		seen[it.VariantID] = true
	}
	return nil
}
func quantities(items []Item) map[string]int64 {
	m := map[string]int64{}
	for _, it := range items {
		m[it.VariantID] += it.Quantity
	}
	return m
}
func fullyPicked(o Order) bool {
	m := quantities(o.Picked)
	for _, it := range o.Items {
		if m[it.VariantID] != it.Quantity {
			return false
		}
	}
	return true
}
func deliveryStatus(ordered, delivered []Item) (string, error) {
	if err := validItems(delivered, true); err != nil {
		return "", err
	}
	m := quantities(ordered)
	for _, it := range delivered {
		if m[it.VariantID] < it.Quantity {
			return "", apperr.Validation("Delivered quantity exceeds the order or variant is not on the order")
		}
	}
	if len(delivered) == 0 {
		return "failed", nil
	}
	if len(ordered) == len(delivered) {
		all := true
		for _, it := range delivered {
			if m[it.VariantID] != it.Quantity {
				all = false
			}
		}
		if all {
			return "delivered", nil
		}
	}
	return "partiallyDelivered", nil
}

func FinancialYear(t time.Time) string {
	ist := time.FixedZone("IST", 5*3600+30*60)
	t = t.In(ist)
	y := t.Year()
	if t.Month() < time.April {
		y--
	}
	return fmt.Sprintf("%02d-%02d", y%100, (y+1)%100)
}
func InvoiceNumber(t time.Time, sequence int64) (string, error) {
	if sequence < 1 || sequence > 99999 {
		return "", apperr.Conflict("INVOICE_SERIES_EXHAUSTED", "Financial year invoice series is exhausted")
	}
	return fmt.Sprintf("INV/%s/%05d", FinancialYear(t), sequence), nil
}

type AllocationBill struct {
	InvoiceID string
	Due       float64
}
type AllocationPart struct {
	InvoiceID string
	Amount    float64
}

// AllocateOldest receives bills already sorted by creation time and invoice number.
// Integer cents prevent fractional arithmetic from creating or losing credit.
func AllocateOldest(amount float64, bills []AllocationBill) ([]AllocationPart, float64) {
	remaining := cents(amount)
	parts := []AllocationPart{}
	for _, b := range bills {
		due := cents(b.Due)
		if due <= 0 || remaining <= 0 {
			continue
		}
		take := due
		if take > remaining {
			take = remaining
		}
		parts = append(parts, AllocationPart{b.InvoiceID, money(take)})
		remaining -= take
	}
	return parts, money(remaining)
}
func ValidatePage(p Page) error {
	if p.Page < 1 || p.Page > 1000000 || p.Limit < 1 || p.Limit > 200 {
		return apperr.Validation("page must be 1 to 1,000,000 and limit must be 1 to 200")
	}
	return nil
}
func allow(a Actor, roles ...string) error {
	for _, r := range roles {
		if a.Role == r {
			return nil
		}
	}
	return apperr.Forbidden()
}
