package store

import (
	"context"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"harbor/internal/apperr"
	"harbor/internal/database"
)

// CustomerOption deliberately excludes contact details and financial information.
type CustomerOption struct {
	CustomerID string `json:"customerId" bson:"customerId"`
	Name       string `json:"name" bson:"name"`
}

func (s *Store) CustomerOptions(ctx context.Context, a Actor, p Page) (List[CustomerOption], error) {
	out := List[CustomerOption]{Items: []CustomerOption{}, Page: p.Page, Limit: p.Limit}
	if err := allow(a, Admin, Manager, WarehouseStaff, DeliveryPartner); err != nil {
		return out, err
	}
	if err := ValidatePage(p); err != nil {
		return out, err
	}
	ctx = database.WithAccount(ctx, a.AccountID)
	filter := bson.M{"isActive": true}
	total, err := s.col("customers").CountDocuments(ctx, filter)
	if err != nil {
		return out, err
	}
	out.Total = total
	cur, err := s.col("customers").Find(ctx, filter, options.Find().
		SetProjection(bson.M{"customerId": 1, "name": 1}).
		SetSort(bson.D{{Key: "name", Value: 1}, {Key: "customerId", Value: 1}}).
		SetSkip(int64(p.Page-1)*int64(p.Limit)).SetLimit(int64(p.Limit)))
	if err != nil {
		return out, err
	}
	defer cur.Close(ctx)
	err = cur.All(ctx, &out.Items)
	return out, err
}

type AssignOrderInput struct {
	TripID            string `json:"tripId"`
	DeliveryPartnerID string `json:"deliveryPartnerId"`
	VehicleID         string `json:"vehicleId"`
}

func validateOrderAssignment(orderID string, in AssignOrderInput) error {
	if strings.TrimSpace(orderID) == "" {
		return apperr.Validation("orderId is required")
	}
	if strings.TrimSpace(in.DeliveryPartnerID) == "" && in.TripID == "" {
		return apperr.Validation("deliveryPartnerId is required")
	}
	return nil
}

// AssignOrder attaches an open order to the rider's current trip even when the
// automatic-order policy is disabled. Stock still changes only during pickup.
func (s *Store) AssignOrder(ctx context.Context, a Actor, orderID string, in AssignOrderInput) (Order, error) {
	var out Order
	if err := validateOrderAssignment(orderID, in); err != nil {
		return out, err
	}
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, _ Actor) error {
		if err := s.lock(sc, "orders", "orderId", orderID, &out); err != nil {
			return err
		}
		if out.Status != "open" || out.TripID != "" || len(out.Picked) != 0 {
			return apperr.Conflict("ORDER_ALREADY_ASSIGNED", "Only an open, unpicked order can be assigned")
		}
		if in.TripID != "" {
			var trip Trip
			if err := s.find(sc, "trips", "tripId", in.TripID, &trip); err != nil {
				return err
			}
			if in.DeliveryPartnerID != "" && trip.DeliveryPartnerID != in.DeliveryPartnerID {
				return apperr.Validation("Trip belongs to a different delivery partner")
			}
			if in.VehicleID != "" && trip.VehicleID != in.VehicleID {
				return apperr.Validation("Trip uses a different vehicle")
			}
			if err := s.attachOrderToTrip(sc, a, in.TripID, &out); err != nil {
				return err
			}
		} else if err := s.assignOrderToPartner(sc, in.DeliveryPartnerID, in.VehicleID, &out); err != nil {
			return err
		}
		rows := []Order{out}
		if err := s.enrichOrderReservations(sc, rows); err != nil {
			return err
		}
		out = rows[0]
		return nil
	})
	return out, err
}
