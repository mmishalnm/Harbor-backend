package store

import (
	"go.mongodb.org/mongo-driver/bson"
	"testing"
	"time"
)

func TestOrderFilterValidationAndIndianDates(t *testing.T) {
	for _, filters := range []map[string]string{{"status": "unknown"}, {"source": "unknown"}, {"dateFrom": "2026-02-30"}, {"dateTo": "today"}, {"dateFrom": "2026-11-20", "dateTo": "2026-11-19"}, {"deliveryPartnerId": "CUS-000001"}} {
		if _, err := orderFilterConditions(filters); err == nil {
			t.Fatal("invalid filter accepted", filters)
		}
	}
	got, err := orderFilterConditions(map[string]string{"status": "assigned", "source": "phone", "dateFrom": "2026-11-19", "dateTo": "2026-11-19", "deliveryPartnerId": "USR-000002"})
	if err != nil {
		t.Fatal(err)
	}
	dates := got["createdAt"].(bson.M)
	start := time.Date(2026, 11, 18, 18, 30, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	if got["status"] != "assigned" || got["source"] != "phone" || !dates["$gte"].(time.Time).Equal(start) || !dates["$lt"].(time.Time).Equal(end) {
		t.Fatal("date range is not inclusive Indian dates", got)
	}
}
func TestMixedCashAllocationOldestSourceFirst(t *testing.T) {
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	sources := []cashSource{{TripID: "TRP-1", CreatedAt: at.Add(time.Hour)}, {PaymentID: "PAY-1", CreatedAt: at}}
	balances := map[string]int64{"TRP-1": 100000, "PAY-1": 100000}
	allocations, left := allocateCashSources(150000, sources, balances)
	if left != 0 || len(allocations) != 2 || allocations[0].PaymentID != "PAY-1" || allocations[0].TripID != "" || allocations[0].Amount != 1000 || allocations[1].TripID != "TRP-1" || allocations[1].PaymentID != "" || allocations[1].Amount != 500 {
		t.Fatal(allocations, left)
	}
	if balances["PAY-1"] != 100000 || sources[0].TripID != "TRP-1" {
		t.Fatal("allocation changed its inputs")
	}
}
