package store

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"harbor/internal/apperr"
)

type OrderSettings struct {
	AutoAssignDeliveryPartnerOrders bool `json:"autoAssignDeliveryPartnerOrders" bson:"autoAssignDeliveryPartnerOrders"`
	TripDurationDays                int  `json:"tripDurationDays" bson:"tripDurationDays"`
}
type OrderSettingsInput struct {
	AutoAssignDeliveryPartnerOrders *bool `json:"autoAssignDeliveryPartnerOrders"`
	TripDurationDays                *int  `json:"tripDurationDays"`
}

func effectiveOrderSettings(in OrderSettings) OrderSettings {
	if in.TripDurationDays == 0 {
		in.TripDurationDays = 1
	}
	return in
}
func validateOrderSettings(in OrderSettingsInput) (OrderSettings, error) {
	if in.AutoAssignDeliveryPartnerOrders == nil || in.TripDurationDays == nil {
		return OrderSettings{}, apperr.Validation("autoAssignDeliveryPartnerOrders and tripDurationDays are required")
	}
	if *in.TripDurationDays < 1 || *in.TripDurationDays > 365 {
		return OrderSettings{}, apperr.Validation("tripDurationDays must be between 1 and 365")
	}
	return OrderSettings{*in.AutoAssignDeliveryPartnerOrders, *in.TripDurationDays}, nil
}
func (s *Store) SetOrderSettings(ctx context.Context, a Actor, in OrderSettingsInput) (OrderSettings, error) {
	settings, err := validateOrderSettings(in)
	if err != nil {
		return OrderSettings{}, err
	}
	err = s.tx(ctx, a, []string{Admin}, func(sc mongo.SessionContext, _ Actor) error {
		return s.update(sc, "accounts", "accountId", a.AccountID, bson.M{"orderSettings": settings})
	})
	return settings, err
}

type DeliveryVehicleInput struct {
	VehicleID *string `json:"vehicleId"`
}

func (s *Store) AssignDeliveryVehicle(ctx context.Context, a Actor, userID string, in DeliveryVehicleInput) (User, error) {
	var out User
	if in.VehicleID == nil {
		return out, apperr.Validation("vehicleId is required; use an empty string to clear the assignment")
	}
	err := s.tx(ctx, a, []string{Admin}, func(sc mongo.SessionContext, _ Actor) error {
		if err := s.lock(sc, "users", "userId", userID, &out); err != nil {
			return err
		}
		if out.Role != DeliveryPartner {
			return apperr.Validation("Only a deliveryPartner can have an automatic-trip vehicle")
		}
		if *in.VehicleID != "" {
			var v Vehicle
			if err := s.lock(sc, "vehicles", "vehicleId", *in.VehicleID, &v); err != nil {
				return err
			}
		}
		if err := s.update(sc, "users", "userId", userID, bson.M{"assignedVehicleId": *in.VehicleID}); err != nil {
			return err
		}
		return s.find(sc, "users", "userId", userID, &out)
	})
	return out, err
}

type TripSchedule struct {
	TripDate     string    `json:"tripDate" bson:"tripDate"`
	DurationDays int       `json:"durationDays" bson:"durationDays"`
	StartsAt     time.Time `json:"startsAt" bson:"startsAt"`
	EndsAt       time.Time `json:"endsAt" bson:"endsAt"`
}

var tripTimezone = time.FixedZone("IST", 5*3600+30*60)

func newTripSchedule(now time.Time, startDate string, days int) (TripSchedule, error) {
	if days < 1 || days > 365 {
		return TripSchedule{}, apperr.Validation("Trip duration must be between 1 and 365 days")
	}
	if startDate == "" {
		startDate = now.In(tripTimezone).Format("2006-01-02")
	}
	start, err := time.ParseInLocation("2006-01-02", startDate, tripTimezone)
	if err != nil {
		return TripSchedule{}, apperr.Validation("startDate must be a valid YYYY-MM-DD date")
	}
	return TripSchedule{startDate, days, start.UTC(), start.AddDate(0, 0, days).UTC()}, nil
}

// Old trips keep operating with a one-day window based on their creation date.
// Scheduling never closes a trip or discards stock at midnight.
func normalizeTrip(t *Trip) {
	if t.DurationDays == 0 {
		t.DurationDays = 1
	}
	if t.StartsAt.IsZero() && !t.CreatedAt.IsZero() {
		schedule, err := newTripSchedule(t.CreatedAt, "", t.DurationDays)
		if err == nil {
			t.TripSchedule = schedule
		}
	}
	if t.EndsAt.IsZero() && !t.StartsAt.IsZero() {
		t.EndsAt = t.StartsAt.AddDate(0, 0, t.DurationDays)
	}
}
func eligibleOrderTrip(t Trip, partnerID string, now time.Time) bool {
	normalizeTrip(&t)
	return t.IsActive && t.DeliveryPartnerID == partnerID && (t.Status == "planned" || t.Status == "started") && !t.StartsAt.IsZero() && !now.Before(t.StartsAt) && now.Before(t.EndsAt)
}
func preferOrderTrip(candidate, current Trip) bool {
	if current.TripID == "" {
		return true
	}
	if candidate.Status != current.Status {
		return candidate.Status == "started"
	}
	if !candidate.StartsAt.Equal(current.StartsAt) {
		return candidate.StartsAt.Before(current.StartsAt)
	}
	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.Before(current.CreatedAt)
	}
	return candidate.TripID < current.TripID
}

func (s *Store) autoAssignOrder(sc mongo.SessionContext, a Actor, order *Order) error {
	var account BusinessAccount
	if err := s.find(sc, "accounts", "accountId", a.AccountID, &account); err != nil {
		return err
	}
	if !account.OrderSettings.AutoAssignDeliveryPartnerOrders {
		return nil
	}
	return s.assignOrderToPartner(sc, a.UserID, "", order)
}

func (s *Store) assignOrderToPartner(sc mongo.SessionContext, partnerID, vehicleID string, order *Order) error {
	// Serialize both automatic and office assignments with this partner's orders.
	var partner User
	if err := s.lock(sc, "users", "userId", partnerID, &partner); err != nil {
		return err
	}
	if partner.Role != DeliveryPartner {
		return apperr.Validation("deliveryPartnerId must identify a deliveryPartner")
	}
	now := s.now().UTC()
	var chosen Trip
	full := false
	cur, err := s.col("trips").Find(sc, bson.M{"deliveryPartnerId": partnerID, "isActive": true, "status": bson.M{"$in": []string{"planned", "started"}}})
	if err != nil {
		return err
	}
	for cur.Next(sc) {
		var candidate Trip
		if err := cur.Decode(&candidate); err != nil {
			_ = cur.Close(sc)
			return err
		}
		normalizeTrip(&candidate)
		if !eligibleOrderTrip(candidate, partnerID, now) || (vehicleID != "" && candidate.VehicleID != vehicleID) {
			continue
		}
		if len(candidate.Stops) >= 200 {
			full = true
			continue
		}
		if preferOrderTrip(candidate, chosen) {
			chosen = candidate
		}
	}
	err = cur.Err()
	_ = cur.Close(sc)
	if err != nil {
		return err
	}
	if chosen.TripID == "" {
		if full {
			return apperr.Conflict("TRIP_FULL", "The delivery partner's current trip has reached its 200-order limit")
		}
		if vehicleID == "" {
			vehicleID = partner.AssignedVehicleID
		}
		if vehicleID == "" {
			return apperr.Conflict("DELIVERY_VEHICLE_NOT_ASSIGNED", "Choose a vehicle or assign a default vehicle to the delivery partner before creating a trip")
		}
		chosen, err = s.createTrip(sc, TripInput{VehicleID: vehicleID, DeliveryPartnerID: partnerID, OrderIDs: []string{}}, now)
		if err != nil {
			return err
		}
	} else {
		if err := s.lock(sc, "trips", "tripId", chosen.TripID, &chosen); err != nil {
			return err
		}
	}
	chosen.Stops = append(chosen.Stops, Stop{OrderID: order.OrderID, Status: "pending", Delivered: []Item{}})
	if err := s.update(sc, "trips", "tripId", chosen.TripID, bson.M{"stops": chosen.Stops}); err != nil {
		return err
	}
	if err := s.update(sc, "orders", "orderId", order.OrderID, bson.M{"tripId": chosen.TripID, "status": "assigned"}); err != nil {
		return err
	}
	return s.find(sc, "orders", "orderId", order.OrderID, order)
}
