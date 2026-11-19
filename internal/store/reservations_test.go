package store

import (
	"reflect"
	"testing"
	"time"
)

func reservationOrder(id string, q int64) Order {
	return Order{Base: Base{IsActive: true, CreatedAt: time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)}, OrderID: id, Status: "open", Items: []Item{{"MVN-1", q}}}
}
func TestReservationTotalsAndOldestFirst(t *testing.T) {
	stocks := []Stock{{StockKey: "LOC-2:MVN-1", VariantID: "MVN-1", Quantity: 125}, {StockKey: "LOC-1:MVN-1", VariantID: "MVN-1", Quantity: 100}}
	plan := buildReservationPlan(stocks, []Order{reservationOrder("ORD-1", 13)})
	if plan.OnHand["MVN-1"] != 225 || plan.Reserved["MVN-1"] != 13 || plan.OnHand["MVN-1"]-plan.Reserved["MVN-1"] != 212 {
		t.Fatal(plan)
	}
	if plan.StockReserved["LOC-1:MVN-1"] != 13 || plan.StockReserved["LOC-2:MVN-1"] != 0 {
		t.Fatal("unstable storage reservation", plan.StockReserved)
	}
	if stocks[0].Quantity != 125 {
		t.Fatal("reservation changed physical stock")
	}
	older, newer := reservationOrder("ORD-1", 4), reservationOrder("ORD-2", 4)
	plan = buildReservationPlan([]Stock{{VariantID: "MVN-1", Quantity: 5}}, []Order{newer, older})
	if plan.Orders[older.OrderID]["MVN-1"] != (reservationLine{4, 0}) || plan.Orders[newer.OrderID]["MVN-1"] != (reservationLine{1, 3}) {
		t.Fatal("order priority or shortage wrong", plan)
	}
	older.Picked = []Item{{"MVN-1", 2}}
	plan = buildReservationPlan([]Stock{{VariantID: "MVN-1", Quantity: 3}}, []Order{older, newer})
	if plan.Orders[older.OrderID]["MVN-1"].Reserved != 2 || plan.Reserved["MVN-1"] != 3 {
		t.Fatal("pickup counted twice", plan)
	}
	older.Status = "cancelled"
	plan = buildReservationPlan([]Stock{{VariantID: "MVN-1", Quantity: 3}}, []Order{older, newer})
	applyOrderReservation(&newer, plan)
	if !reflect.DeepEqual(newer.ReservedItems, []Item{{"MVN-1", 3}}) || !reflect.DeepEqual(newer.AwaitingStockItems, []Item{{"MVN-1", 1}}) {
		t.Fatal("cancel did not release reservation", newer)
	}
	plan = buildReservationPlan(nil, []Order{reservationOrder("ORD-3", 7)})
	if plan.Reserved["MVN-1"] != 0 || plan.Orders["ORD-3"]["MVN-1"].Awaiting != 7 {
		t.Fatal("negative availability or missing backorder", plan)
	}
}
func TestBulkOrderValidationAndTripNames(t *testing.T) {
	for _, ids := range [][]string{nil, {}, {""}, {"ORD-1", "ORD-1"}, make([]string, 201)} {
		if validateOrderIDs(ids) == nil {
			t.Fatal("invalid batch accepted", ids)
		}
	}
	if err := validateOrderIDs([]string{"ORD-1", "ORD-2"}); err != nil {
		t.Fatal(err)
	}
	if defaultTripName("TRP-000001") == defaultTripName("TRP-000002") {
		t.Fatal("same-day trips have duplicate default names")
	}
}
