package store

import (
	"context"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"harbor/internal/apperr"
	"harbor/internal/database"
)

type OrderInput struct {
	CustomerID string `json:"customerId"`
	Source     string `json:"source"`
	Items      []Item `json:"items"`
}

func validateOrder(in OrderInput) error {
	if err := validItems(in.Items, false); err != nil {
		return err
	}
	switch in.Source {
	case "counter", "salesStaff", "store", "phone", "pickup", "deliveryPartner":
		return nil
	}
	return apperr.Validation("source must be counter, salesStaff, store, phone, pickup, or deliveryPartner")
}
func (s *Store) newOrder(sc mongo.SessionContext, a Actor, in OrderInput) (Order, error) {
	var out Order
	if err := validateOrder(in); err != nil {
		return out, err
	}
	var c Customer
	if err := s.lock(sc, "customers", "customerId", in.CustomerID, &c); err != nil {
		return out, err
	}
	for _, it := range in.Items {
		var v Variant
		if err := s.lock(sc, "variants", "variantId", it.VariantID, &v); err != nil {
			return out, err
		}
	}
	id, err := s.next(sc, "order", "ORD-")
	if err != nil {
		return out, err
	}
	out = Order{Base: s.base(), OrderID: id, CustomerID: in.CustomerID, Source: in.Source, Items: in.Items, Picked: []Item{}, Status: "open", CreatedBy: a.UserID}
	return out, insert(sc, s.col("orders"), out)
}
func (s *Store) CreateOrder(ctx context.Context, a Actor, in OrderInput) (Order, error) {
	var out Order
	err := s.tx(ctx, a, []string{Admin, Manager, WarehouseStaff, DeliveryPartner}, func(sc mongo.SessionContext, a Actor) error {
		if a.Role == DeliveryPartner && in.Source == "" {
			in.Source = "deliveryPartner"
		}
		var err error
		out, err = s.newOrder(sc, a, in)
		if err != nil {
			return err
		}
		if a.Role == DeliveryPartner {
			return s.autoAssignOrder(sc, a, &out)
		}
		return nil
	})
	return out, err
}

type TripInput struct {
	StartDate         string   `json:"startDate"`
	WarehouseStaffIDs []string `json:"warehouseStaffIds"`
	VehicleID         string   `json:"vehicleId"`
	DeliveryPartnerID string   `json:"deliveryPartnerId"`
	OrderIDs          []string `json:"orderIds"`
}

func (s *Store) CreateTrip(ctx context.Context, a Actor, in TripInput) (Trip, error) {
	var out Trip
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, _ Actor) error {
		var err error
		out, err = s.createTrip(sc, in, s.now().UTC())
		return err
	})
	return out, err
}
func (s *Store) createTrip(sc mongo.SessionContext, in TripInput, now time.Time) (Trip, error) {
	var out Trip
	if len(in.OrderIDs) > 200 {
		return out, apperr.Validation("A trip supports at most 200 orders")
	}
	aid, err := database.AccountID(sc)
	if err != nil {
		return out, err
	}
	var account BusinessAccount
	if err := s.find(sc, "accounts", "accountId", aid, &account); err != nil {
		return out, err
	}
	schedule, err := newTripSchedule(now, in.StartDate, account.OrderSettings.TripDurationDays)
	if err != nil {
		return out, err
	}

	if err := s.validateTripStaff(sc, in.WarehouseStaffIDs); err != nil {
		return out, err
	}
	var v Vehicle
	if err := s.lock(sc, "vehicles", "vehicleId", in.VehicleID, &v); err != nil {
		return out, err
	}
	var u User
	if err := s.lock(sc, "users", "userId", in.DeliveryPartnerID, &u); err != nil {
		return out, err
	}
	if u.Role != DeliveryPartner {
		return out, apperr.Validation("deliveryPartnerId must identify a deliveryPartner")
	}
	n, err := s.col("trips").CountDocuments(sc, bson.M{"vehicleId": in.VehicleID, "vehicleReserved": true})
	if err != nil {
		return out, err
	}
	if n > 0 {
		return out, apperr.Conflict("VEHICLE_BUSY", "Vehicle is already assigned to an unfinished trip")
	}
	n, err = s.col("stocks").CountDocuments(sc, bson.M{"locationId": in.VehicleID, "quantity": bson.M{"$ne": 0}})
	if err != nil {
		return out, err
	}
	if n > 0 {
		return out, apperr.Conflict("VAN_NOT_EMPTY", "Vehicle must be empty before a new trip")
	}
	id, err := s.next(sc, "trip", "TRP-")
	if err != nil {
		return out, err
	}
	staffIDs := in.WarehouseStaffIDs
	if staffIDs == nil {
		staffIDs = []string{}
	}
	out = Trip{TripSchedule: schedule, WarehouseStaffIDs: staffIDs, Base: s.base(), TripID: id, VehicleID: in.VehicleID, DeliveryPartnerID: in.DeliveryPartnerID, Status: "planned", Stops: []Stop{}, VehicleReserved: true}
	seen := map[string]bool{}
	for _, oid := range in.OrderIDs {
		if seen[oid] {
			return out, apperr.Validation("Duplicate orderId")
		}
		seen[oid] = true
		var o Order
		if err := s.lock(sc, "orders", "orderId", oid, &o); err != nil {
			return out, err
		}
		if o.Status != "open" || o.TripID != "" {
			return out, apperr.Conflict("ORDER_ALREADY_ASSIGNED", "Order is already assigned or closed")
		}
		if err := s.update(sc, "orders", "orderId", oid, bson.M{"tripId": id, "status": "assigned"}); err != nil {
			return out, err
		}
		out.Stops = append(out.Stops, Stop{OrderID: oid, Status: "pending", Delivered: []Item{}})
	}
	return out, insert(sc, s.col("trips"), out)
}

type PickupInput struct {
	OrderID    string      `json:"orderId"`
	Order      *OrderInput `json:"order,omitempty"`
	LocationID string      `json:"locationId"`
	Items      []Item      `json:"items"`
}
type PickupResult struct {
	TripID    string     `json:"tripId"`
	Order     Order      `json:"order"`
	Movements []Movement `json:"movements"`
}

func (s *Store) Pickup(ctx context.Context, a Actor, tripID string, in PickupInput) (PickupResult, error) {
	var out PickupResult
	if err := validItems(in.Items, false); err != nil {
		return out, err
	}
	if (in.OrderID == "") == (in.Order == nil) {
		return out, apperr.Validation("Provide exactly one of orderId or order")
	}
	err := s.tx(ctx, a, []string{Admin, Manager, WarehouseStaff, DeliveryPartner}, func(sc mongo.SessionContext, a Actor) error {
		out = PickupResult{TripID: tripID, Movements: []Movement{}}
		var t Trip
		if err := s.lock(sc, "trips", "tripId", tripID, &t); err != nil {
			return err
		}
		if err := s.tripAccess(a, t); err != nil {
			return err
		}
		if t.Status != "planned" && t.Status != "started" {
			return apperr.Conflict("TRIP_NOT_OPEN", "Pickup requires a planned or started trip")
		}
		if _, err := s.storage(sc, in.LocationID); err != nil {
			return err
		}
		if err := checkPickup(a, in.LocationID); err != nil {
			return err
		}
		var o Order
		if in.Order != nil {
			if len(t.Stops) >= 200 {
				return apperr.Validation("A trip supports at most 200 orders")
			}
			var err error
			o, err = s.newOrder(sc, a, *in.Order)
			if err != nil {
				return err
			}
			o.TripID = tripID
			o.Status = "assigned"
			t.Stops = append(t.Stops, Stop{OrderID: o.OrderID, Status: "pending", Delivered: []Item{}})
			if err := s.update(sc, "trips", "tripId", tripID, bson.M{"stops": t.Stops}); err != nil {
				return err
			}
		} else {
			if err := s.lock(sc, "orders", "orderId", in.OrderID, &o); err != nil {
				return err
			}
		}
		if o.TripID != tripID || o.Status != "assigned" {
			return apperr.Validation("Order must be assigned to this trip")
		}
		ordered, picked := quantities(o.Items), quantities(o.Picked)
		for _, it := range in.Items {
			if ordered[it.VariantID] == 0 || picked[it.VariantID]+it.Quantity > ordered[it.VariantID] {
				return apperr.Validation("Picked quantity exceeds order quantity or variant is not on the order")
			}
			var v Variant
			if err := s.lock(sc, "variants", "variantId", it.VariantID, &v); err != nil {
				return err
			}
			m, err := s.move(sc, a, it.VariantID, it.Quantity, in.LocationID, t.VehicleID, "pickup", o.OrderID, t.TripID, "")
			if err != nil {
				return err
			}
			out.Movements = append(out.Movements, m)
			picked[it.VariantID] += it.Quantity
		}
		o.Picked = []Item{}
		for _, it := range o.Items {
			if q := picked[it.VariantID]; q > 0 {
				o.Picked = append(o.Picked, Item{it.VariantID, q})
			}
		}
		if err := s.update(sc, "orders", "orderId", o.OrderID, bson.M{"tripId": tripID, "status": "assigned", "picked": o.Picked}); err != nil {
			return err
		}
		out.Order = o
		return nil
	})
	return out, err
}
func (s *Store) StartTrip(ctx context.Context, a Actor, id string) (Trip, error) {
	var out Trip
	err := s.tx(ctx, a, []string{Admin, Manager, DeliveryPartner}, func(sc mongo.SessionContext, a Actor) error {
		if err := s.lock(sc, "trips", "tripId", id, &out); err != nil {
			return err
		}
		if err := s.tripAccess(a, out); err != nil {
			return err
		}
		if out.Status != "planned" {
			return apperr.Conflict("TRIP_NOT_PLANNED", "Only planned trips can start")
		}
		if len(out.Stops) == 0 {
			return apperr.Validation("A trip must contain at least one order")
		}
		for _, st := range out.Stops {
			var o Order
			if err := s.find(sc, "orders", "orderId", st.OrderID, &o); err != nil {
				return err
			}
			if !fullyPicked(o) {
				return apperr.Conflict("ORDER_NOT_FULLY_PICKED", "Every order must be fully picked before the trip starts")
			}
		}
		out.Status = "started"
		return s.update(sc, "trips", "tripId", id, bson.M{"status": out.Status})
	})
	return out, err
}

type CloseStopInput struct {
	Delivered       []Item  `json:"delivered"`
	Reason          string  `json:"reason"`
	CollectedAmount float64 `json:"collectedAmount"`
	PaymentMethod   string  `json:"paymentMethod"`
}
type CloseStopResult struct {
	Stop    Stop     `json:"stop"`
	Invoice *Invoice `json:"invoice,omitempty"`
	Payment *Payment `json:"payment,omitempty"`
}

func (s *Store) CloseStop(ctx context.Context, a Actor, tripID, orderID string, in CloseStopInput) (CloseStopResult, error) {
	var out CloseStopResult
	if in.Delivered == nil {
		return out, apperr.Validation("delivered must be an array; use [] for failed delivery")
	}
	if err := validMoney(in.CollectedAmount, true); err != nil {
		return out, err
	}
	if err := validText(in.Reason); err != nil {
		return out, err
	}
	if in.CollectedAmount > 0 && !validMethod(in.PaymentMethod) {
		return out, apperr.Validation("paymentMethod must be cash, upi, or bank")
	}
	err := s.tx(ctx, a, []string{Admin, Manager, DeliveryPartner}, func(sc mongo.SessionContext, a Actor) error {
		out = CloseStopResult{}
		var t Trip
		if err := s.find(sc, "trips", "tripId", tripID, &t); err != nil {
			return err
		}
		if err := s.tripAccess(a, t); err != nil {
			return err
		}
		var stop *Stop
		for i := range t.Stops {
			if t.Stops[i].OrderID == orderID {
				stop = &t.Stops[i]
				break
			}
		}
		if stop == nil {
			return apperr.NotFound()
		}
		if stop.Status != "pending" {
			return apperr.Conflict("STOP_ALREADY_CLOSED", "This stop has already been closed")
		}
		if t.Status != "started" {
			return apperr.Conflict("TRIP_NOT_STARTED", "Trip must be started before closing stops")
		}
		// This claim is rolled back with every stock, invoice, and payment write
		// on failure; concurrent requests cannot both close the same stop.
		r, err := s.col("trips").UpdateOne(sc, bson.M{"tripId": tripID, "status": "started", "stops": bson.M{"$elemMatch": bson.M{"orderId": orderID, "status": "pending"}}}, bson.M{"$set": bson.M{"stops.$.status": "closing"}, "$inc": bson.M{"revision": 1}})
		if err != nil {
			return err
		}
		if r.MatchedCount == 0 {
			return apperr.Conflict("STOP_ALREADY_CLOSED", "This stop has already been closed")
		}
		var o Order
		if err := s.lock(sc, "orders", "orderId", orderID, &o); err != nil {
			return err
		}
		if len(in.Delivered) > 0 && !fullyPicked(o) {
			return apperr.Conflict("ORDER_NOT_FULLY_PICKED", "Pick every order item before delivering this stop")
		}
		status, err := deliveryStatus(o.Items, in.Delivered)
		if err != nil {
			return err
		}
		if status != "delivered" && strings.TrimSpace(in.Reason) == "" {
			return apperr.Validation("A reason is required for partial or failed delivery")
		}
		var c Customer
		if err := s.lockCustomerLedger(sc, o.CustomerID, &c); err != nil {
			return err
		}
		if len(in.Delivered) > 0 {
			inv, err := s.createInvoice(sc, a, t, o, in.Delivered)
			if err != nil {
				return err
			}
			out.Invoice = &inv
			stop.InvoiceID = inv.InvoiceID
			for _, it := range in.Delivered {
				if _, err := s.move(sc, a, it.VariantID, it.Quantity, t.VehicleID, "", "delivery", inv.InvoiceID, t.TripID, ""); err != nil {
					return err
				}
			}
		}
		if RoundMoney(in.CollectedAmount) > 0 {
			p, err := s.recordPayment(sc, a, o.CustomerID, tripID, in.CollectedAmount, in.PaymentMethod)
			if err != nil {
				return err
			}
			out.Payment = &p
		}
		if err := s.allocateCredit(sc, o.CustomerID); err != nil {
			return err
		}
		now := s.now().UTC()
		stop.Status = status
		stop.Delivered = in.Delivered
		if stop.Delivered == nil {
			stop.Delivered = []Item{}
		}
		stop.Reason = in.Reason
		stop.ClosedAt = &now
		out.Stop = *stop
		if err := s.update(sc, "orders", "orderId", orderID, bson.M{"status": status}); err != nil {
			return err
		}
		return s.update(sc, "trips", "tripId", tripID, bson.M{"stops": t.Stops})
	})
	return out, err
}

type ReturnInput struct {
	LocationID string `json:"locationId"`
	Items      []Item `json:"items"`
	Reason     string `json:"reason"`
}

func (s *Store) ReturnStock(ctx context.Context, a Actor, id string, in ReturnInput) ([]Movement, error) {
	out := []Movement{}
	if err := validItems(in.Items, false); err != nil {
		return out, err
	}
	if err := validText(in.Reason); err != nil {
		return out, err
	}
	err := s.tx(ctx, a, []string{Admin, Manager, WarehouseStaff, DeliveryPartner}, func(sc mongo.SessionContext, a Actor) error {
		out = []Movement{}
		var t Trip
		if err := s.lock(sc, "trips", "tripId", id, &t); err != nil {
			return err
		}
		if err := s.tripAccess(a, t); err != nil {
			return err
		}
		if t.Status != "started" {
			return apperr.Conflict("TRIP_NOT_STARTED", "Returns require a started trip")
		}
		for _, st := range t.Stops {
			if st.Status == "pending" {
				return apperr.Conflict("STOPS_OPEN", "Close every stop before returning leftovers")
			}
		}
		if _, err := s.storage(sc, in.LocationID); err != nil {
			return err
		}
		for _, it := range in.Items {
			m, err := s.move(sc, a, it.VariantID, it.Quantity, t.VehicleID, in.LocationID, "return", id, id, in.Reason)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return nil
	})
	return out, err
}
func (s *Store) CompleteTrip(ctx context.Context, a Actor, id string) (Trip, error) {
	var out Trip
	err := s.tx(ctx, a, []string{Admin, Manager, DeliveryPartner}, func(sc mongo.SessionContext, a Actor) error {
		if err := s.lock(sc, "trips", "tripId", id, &out); err != nil {
			return err
		}
		if err := s.tripAccess(a, out); err != nil {
			return err
		}
		if out.Status != "started" {
			return apperr.Conflict("TRIP_NOT_STARTED", "Only started trips can be completed")
		}
		for _, st := range out.Stops {
			if st.Status == "pending" {
				return apperr.Conflict("STOPS_OPEN", "Every stop must be closed")
			}
		}
		n, err := s.col("stocks").CountDocuments(sc, bson.M{"locationId": out.VehicleID, "quantity": bson.M{"$ne": 0}})
		if err != nil {
			return err
		}
		if n > 0 {
			return apperr.Conflict("VAN_NOT_EMPTY", "Return all leftover vehicle stock before completing the trip")
		}
		out.Status = "completed"
		out.VehicleReserved = false
		return s.update(sc, "trips", "tripId", id, bson.M{"status": out.Status, "vehicleReserved": false})
	})
	return out, err
}

type SettlementInput struct {
	CashHandedOver *float64 `json:"cashHandedOver"`
	Note           string   `json:"note"`
}

func (s *Store) Settle(ctx context.Context, a Actor, id string, in SettlementInput) (Settlement, error) {
	var out Settlement
	if in.CashHandedOver == nil {
		return out, apperr.Validation("cashHandedOver is required")
	}
	if err := validMoney(*in.CashHandedOver, true); err != nil {
		return out, err
	}
	if err := validText(in.Note); err != nil {
		return out, err
	}
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, a Actor) error {
		var t Trip
		if err := s.lock(sc, "trips", "tripId", id, &t); err != nil {
			return err
		}
		if t.Status == "settled" {
			return apperr.Conflict("TRIP_ALREADY_SETTLED", "Trip has already been settled")
		}
		if t.Status != "completed" {
			return apperr.Conflict("TRIP_NOT_COMPLETED", "Complete the trip before settlement")
		}
		cur, err := s.col("payments").Find(sc, bson.M{"tripId": id, "method": "cash"})
		if err != nil {
			return err
		}
		defer cur.Close(sc)
		total := int64(0)
		for cur.Next(sc) {
			var p Payment
			if err := cur.Decode(&p); err != nil {
				return err
			}
			total += cents(p.Amount)
		}
		if err := cur.Err(); err != nil {
			return err
		}
		collected := money(total)
		handed := RoundMoney(*in.CashHandedOver)
		difference := money(cents(handed) - cents(collected))
		out = Settlement{id, collected, handed, difference, difference != 0, in.Note, a.UserID, s.now().UTC()}
		if err := insert(sc, s.col("settlements"), out); err != nil {
			return err
		}
		return s.update(sc, "trips", "tripId", id, bson.M{"status": "settled"})
	})
	return out, err
}
func (s *Store) TripOrders(ctx context.Context, a Actor, id string, p Page) (List[Order], error) {
	ctx = database.WithAccount(ctx, a.AccountID)
	if _, err := s.GetTrip(ctx, a, id); err != nil {
		return List[Order]{}, err
	}
	return list[Order](ctx, s, "orders", bson.M{"tripId": id}, p, bson.D{{Key: "orderId", Value: 1}})
}
func (s *Store) TripStock(ctx context.Context, a Actor, id string, p Page) (List[Stock], error) {
	ctx = database.WithAccount(ctx, a.AccountID)
	out := List[Stock]{Items: []Stock{}, Page: p.Page, Limit: p.Limit}
	if err := ValidatePage(p); err != nil {
		return out, err
	}
	if err := allow(a, Admin, Manager, WarehouseStaff, DeliveryPartner); err != nil {
		return out, err
	}
	err := s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		out = List[Stock]{Items: []Stock{}, Page: p.Page, Limit: p.Limit}
		var t Trip
		if err := s.find(sc, "trips", "tripId", id, &t); err != nil {
			return err
		}
		if err := s.tripAccess(a, t); err != nil {
			return err
		}
		// A reused vehicle belongs to its new trip. Closed trips expose no
		// current vehicle inventory, even when the old partner can view them.
		if !t.VehicleReserved {
			return nil
		}
		var err error
		out, err = list[Stock](sc, s, "stocks", bson.M{"locationId": t.VehicleID}, p, bson.D{{Key: "stockKey", Value: 1}})
		return err
	})
	return out, err
}
