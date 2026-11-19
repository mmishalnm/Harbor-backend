package store

import (
	"math"
	"testing"
)

func TestCashHeldAndSettlementValidation(t *testing.T) {
	if held, over := cashHeld(350000, 100000, 150000); held != 100000 || over != 0 {
		t.Fatal(held, over)
	}
	if held, over := cashHeld(10000, 12000, 0); held != 0 || over != 2000 {
		t.Fatal(held, over)
	}
	good := PartnerCashInput{Amount: 500, Method: "salaryAdjustment", Note: "November salary", RequestID: "cash-request-00001"}
	if err := validatePartnerCash(good); err != nil {
		t.Fatal(err)
	}
	for _, amount := range []float64{0, -1, math.NaN(), math.Inf(1), 1e10, 0.001} {
		bad := good
		bad.Amount = amount
		if validatePartnerCash(bad) == nil {
			t.Fatal("invalid amount accepted", amount)
		}
	}
	for _, mutate := range []func(*PartnerCashInput){func(p *PartnerCashInput) { p.Method = "other" }, func(p *PartnerCashInput) { p.Note = " " }, func(p *PartnerCashInput) { p.RequestID = "" }, func(p *PartnerCashInput) { due := 100.0; p.SalaryDue = &due }} {
		bad := good
		mutate(&bad)
		if validatePartnerCash(bad) == nil {
			t.Fatal("invalid settlement accepted", bad)
		}
	}
	entry := PartnerCashEntry{PartnerID: "USR-2", Amount: 500, Method: good.Method, Note: good.Note}
	if !sameCashRequest(entry, "USR-2", good) {
		t.Fatal("matching retry rejected")
	}
	good.Amount = 501
	if sameCashRequest(entry, "USR-2", good) {
		t.Fatal("changed retry accepted")
	}
}
