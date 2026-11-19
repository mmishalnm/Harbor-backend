package api

import (
	"sync"
	"testing"

	"harbor/internal/store"
)

func cashLedger(t *testing.T, c testClient, id string) store.PartnerCashLedger {
	t.Helper()
	return must[store.PartnerCashLedger](t, c, "GET", "/api/v1/partners/"+id+"/cash?page=1&limit=20", nil, 200)
}
func TestMongoPartnerCollectionsCashSalaryAndTripSettlement(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 1}, 201)
	_, trip := f.deliver(t, f.customer.CustomerID, 1, 3500)
	rider := loginAs(t, c, "driver")
	must[store.Payment](t, rider, "POST", "/api/v1/payments", store.PaymentInput{CustomerID: f.customer.CustomerID, TripID: trip.TripID, Amount: 500, Method: "upi"}, 201)
	before := must[store.Ledger](t, c, "GET", "/api/v1/customers/"+f.customer.CustomerID+"/ledger", nil, 200)
	ledger := cashLedger(t, c, f.driver.UserID)
	if ledger.Summary.TotalCollected != 4000 || ledger.Summary.CashHeld != 3500 || ledger.Summary.UpiCollected != 500 {
		t.Fatal("digital payment counted as held cash", ledger.Summary)
	}
	own := cashLedger(t, rider, f.driver.UserID)
	if own.Summary.CashHeld != 3500 {
		t.Fatal("partner cannot see own holding")
	}
	list := must[store.List[store.PartnerCashSummary]](t, c, "GET", "/api/v1/partners/cash", nil, 200)
	if list.Total != 1 || list.Items[0].CashCollected != 3500 {
		t.Fatal("partner collection summary missing", list)
	}
	path := "/api/v1/partners/" + f.driver.UserID + "/cash-settlements"
	handover := store.PartnerCashInput{Amount: 1000, Method: "cashHandover", Note: "Cash received at office", Reference: "Receipt 42", RequestID: "handover-request-0001"}
	wantError(t, rider, "POST", path, handover, 403, "FORBIDDEN")
	paid := must[store.PartnerCashResult](t, c, "POST", path, handover, 201)
	if paid.Summary.CashHeld != 2500 || paid.Summary.CashHandedOver != 1000 || paid.Entry.RecordedBy == "" || len(paid.Entry.Allocations) != 1 {
		t.Fatal(paid)
	}
	due := 10000.0
	salary := store.PartnerCashInput{Amount: 1500, Method: "salaryAdjustment", Note: "Adjusted against November salary", Reference: "November 2026", RequestID: "salary-request-000001", SalaryDue: &due}
	adjusted := must[store.PartnerCashResult](t, c, "POST", path, salary, 201)
	if adjusted.Summary.CashHeld != 1000 || adjusted.Summary.SalaryAdjusted != 1500 || adjusted.Entry.SalaryPayable == nil || *adjusted.Entry.SalaryPayable != 8500 {
		t.Fatal("salary offset wrong", adjusted)
	}
	replay := must[store.PartnerCashResult](t, c, "POST", path, salary, 201)
	if replay.Entry.EntryID != adjusted.Entry.EntryID || replay.Summary.CashHeld != 1000 {
		t.Fatal("retry deducted twice", replay)
	}
	changed := salary
	changed.Note = "Changed details"
	wantError(t, c, "POST", path, changed, 409, "IDEMPOTENCY_CONFLICT")
	missing := handover
	missing.Note = " "
	missing.RequestID = "note-required-0001"
	wantError(t, c, "POST", path, missing, 400, "VALIDATION_ERROR")
	summary := must[store.TripCashSummary](t, c, "GET", "/api/v1/trips/"+trip.TripID+"/cash-summary", nil, 200)
	if summary.RemainingCash != 1000 || summary.PriorCashHandedOver != 1000 || summary.SalaryAdjusted != 1500 {
		t.Fatal("trip summary double counted", summary)
	}
	must[store.Trip](t, rider, "POST", "/api/v1/trips/"+trip.TripID+"/complete", nil, 200)
	remaining := 1000.0
	settled := must[store.Settlement](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/settle", store.SettlementInput{CashHandedOver: &remaining, Note: "Remaining cash received; prior salary offset included"}, 201)
	if settled.Difference != 0 || settled.PriorCashHandedOver != 1000 || settled.SalaryAdjusted != 1500 {
		t.Fatal("final trip cash accounting wrong", settled)
	}
	ledger = cashLedger(t, c, f.driver.UserID)
	if ledger.Summary.CashHeld != 0 || ledger.Summary.CashHandedOver != 2000 || ledger.Summary.SalaryAdjusted != 1500 {
		t.Fatal("holding deducted twice", ledger.Summary)
	}
	wantError(t, c, "POST", path, store.PartnerCashInput{Amount: 1, Method: "cashHandover", Note: "Too much", RequestID: "too-much-request-0001"}, 409, "CASH_HOLDING_EXCEEDED")
	after := must[store.Ledger](t, c, "GET", "/api/v1/customers/"+f.customer.CustomerID+"/ledger", nil, 200)
	if after.PaymentTotal != before.PaymentTotal || after.Balance != before.Balance {
		t.Fatal("cash holding settlement altered customer ledger")
	}
	foreign, _ := signupBusiness(t, c, "cashforeign", "Foreign cash business")
	ff := setupFixture(t, foreign, "cashforeign")
	if cashLedger(t, foreign, ff.driver.UserID).Summary.CashHeld != 0 {
		t.Fatal("partner holdings crossed businesses")
	}
	other := createUser(t, c, "cashotherrider", store.DeliveryPartner)
	wantError(t, rider, "GET", "/api/v1/partners/"+other.UserID+"/cash", nil, 403, "FORBIDDEN")
	wantError(t, rider, "GET", "/api/v1/partners/cash", nil, 403, "FORBIDDEN")
}
func TestMongoPartnerCashConcurrentSettlementAndArchive(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 1}, 201)
	_, trip := f.deliver(t, f.customer.CustomerID, 1, 2000)
	createUser(t, c, "cashmanager", store.Manager)
	manager := loginAs(t, c, "cashmanager")
	path := "/api/v1/partners/" + f.driver.UserID + "/cash-settlements"
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for i, client := range []testClient{c, manager} {
		wg.Add(1)
		go func(i int, client testClient) {
			defer wg.Done()
			key := []string{"parallel-cash-000001", "parallel-cash-000002"}[i]
			code, _ := client.call("POST", path, store.PartnerCashInput{Amount: 1500, Method: "cashHandover", Note: "Office cash received", RequestID: key})
			codes <- code
		}(i, client)
	}
	wg.Wait()
	close(codes)
	success, conflict := 0, 0
	for code := range codes {
		if code == 201 {
			success++
		} else if code == 409 {
			conflict++
		} else {
			t.Fatal(code)
		}
	}
	if success != 1 || conflict != 1 || cashLedger(t, c, f.driver.UserID).Summary.CashHeld != 500 {
		t.Fatal("concurrent over-settlement", success, conflict)
	}
	must[store.Trip](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/complete", nil, 200)
	zero := 0.0
	must[store.Settlement](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/settle", store.SettlementInput{CashHandedOver: &zero, Note: "500 still held"}, 201)
	must[map[string]bool](t, c, "POST", "/api/v1/users/"+f.driver.UserID+"/archive", nil, 200)
	settled := must[store.PartnerCashResult](t, c, "POST", path, store.PartnerCashInput{Amount: 500, Method: "cashHandover", Note: "Final handover after leaving", RequestID: "archived-partner-final"}, 201)
	if settled.Summary.CashHeld != 0 || settled.Summary.IsActive {
		t.Fatal("archived obligation not reconcilable", settled)
	}
}
