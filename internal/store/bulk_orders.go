package store

import (
	"context"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"harbor/internal/apperr"
)

type BulkAssignOrdersInput struct {
	OrderIDs          []string `json:"orderIds"`
	DeliveryPartnerID string   `json:"deliveryPartnerId"`
	VehicleID         string   `json:"vehicleId"`
	TripID            string   `json:"tripId"`
}
type BulkOrdersResult struct {
	Orders []Order `json:"orders"`
	TripID string  `json:"tripId,omitempty"`
}
type BulkCancelOrdersInput struct {
	OrderIDs []string `json:"orderIds"`
	Reason   string   `json:"reason"`
}

func validateOrderIDs(ids []string) error {
	if len(ids) < 1 || len(ids) > 200 {
		return apperr.Validation("Choose between 1 and 200 orders")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return apperr.Validation("Each orderId is required")
		}
		if seen[id] {
			return apperr.Validation("Duplicate orderId")
		}
		seen[id] = true
	}
	return nil
}
func (s *Store) BulkAssignOrders(ctx context.Context, a Actor, in BulkAssignOrdersInput) (BulkOrdersResult, error) {
	var out BulkOrdersResult
	if err := validateOrderIDs(in.OrderIDs); err != nil {
		return out, err
	}
	if in.TripID == "" && strings.TrimSpace(in.DeliveryPartnerID) == "" {
		return out, apperr.Validation("Choose a delivery partner or an existing trip")
	}
	err := s.tx(ctx, a, []string{Admin, Manager, DeliveryPartner}, func(sc mongo.SessionContext, a Actor) error {
		out = BulkOrdersResult{Orders: []Order{}, TripID: in.TripID}
		if a.Role == DeliveryPartner && in.TripID == "" {
			return apperr.Validation("Choose one of your trips")
		}
		for _, id := range in.OrderIDs {
			var order Order
			if err := s.lock(sc, "orders", "orderId", id, &order); err != nil {
				return err
			}
			if order.Status != "open" || order.TripID != "" || len(order.Picked) > 0 {
				return apperr.Conflict("ORDER_ALREADY_ASSIGNED", "Only open, unpicked orders can be assigned")
			}
			if out.TripID != "" {
				var t Trip
				if err := s.find(sc, "trips", "tripId", out.TripID, &t); err != nil {
					return err
				}
				if err := s.tripAccess(a, t); err != nil {
					return err
				}
				if in.DeliveryPartnerID != "" && t.DeliveryPartnerID != in.DeliveryPartnerID {
					return apperr.Validation("Trip belongs to a different delivery partner")
				}
				if in.VehicleID != "" && t.VehicleID != in.VehicleID {
					return apperr.Validation("Trip uses a different vehicle")
				}
				if err := s.attachOrderToTrip(sc, a, out.TripID, &order); err != nil {
					return err
				}
			} else {
				if err := allow(a, Admin, Manager); err != nil {
					return err
				}
				if err := s.assignOrderToPartner(sc, in.DeliveryPartnerID, in.VehicleID, &order); err != nil {
					return err
				}
				out.TripID = order.TripID
			}
			out.Orders = append(out.Orders, order)
		}
		return s.enrichOrderReservations(sc, out.Orders)
	})
	return out, err
}
func (s *Store) BulkCancelOrders(ctx context.Context, a Actor, in BulkCancelOrdersInput) (BulkOrdersResult, error) {
	var out BulkOrdersResult
	if err := validateOrderIDs(in.OrderIDs); err != nil {
		return out, err
	}
	if strings.TrimSpace(in.Reason) == "" {
		return out, apperr.Validation("A cancellation reason is required")
	}
	if err := validText(in.Reason); err != nil {
		return out, err
	}
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, a Actor) error {
		out = BulkOrdersResult{Orders: []Order{}}
		for _, id := range in.OrderIDs {
			var order Order
			if err := s.lock(sc, "orders", "orderId", id, &order); err != nil {
				return err
			}
			if order.Status != "open" || order.TripID != "" || len(order.Picked) > 0 {
				return apperr.Conflict("ORDER_NOT_CANCELLABLE", "Only open, unassigned and unpicked orders can be cancelled")
			}
			for _, item := range order.Items {
				var variant Variant
				if err := s.lock(sc, "variants", "variantId", item.VariantID, &variant); err != nil {
					return err
				}
			}
			now := s.now().UTC()
			fields := bson.M{"status": "cancelled", "cancelReason": in.Reason, "cancelledBy": a.UserID, "cancelledAt": now}
			if err := s.update(sc, "orders", "orderId", id, fields); err != nil {
				return err
			}
			order.Status = "cancelled"
			order.CancelReason = in.Reason
			order.CancelledBy = a.UserID
			order.CancelledAt = &now
			order.ReservedItems = []Item{}
			order.AwaitingStockItems = []Item{}
			out.Orders = append(out.Orders, order)
		}
		return nil
	})
	return out, err
}
