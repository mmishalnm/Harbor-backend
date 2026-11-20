package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"harbor/internal/apperr"
	"harbor/internal/database"
)

const DeliveryFulfillment = "delivery"
const CustomerPickupFulfillment = "customerPickup"

func (s *Store) GetOrder(ctx context.Context, a Actor, id string) (Order, error) {
	var out Order
	if err := allow(a, Admin, Manager, WarehouseStaff, DeliveryPartner); err != nil {
		return out, err
	}
	ctx = database.WithAccount(ctx, a.AccountID)
	err := s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		filter := bson.M{"orderId": id}
		if err := s.restrictAssignedLists(sc, a, "orders", filter); err != nil {
			return err
		}
		if err := s.col("orders").FindOne(sc, filter).Decode(&out); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return apperr.NotFound()
			}
			return err
		}
		normalizeRecord(&out)
		rows := []Order{out}
		if err := s.enrichOrderReservations(sc, rows); err != nil {
			return err
		}
		out = rows[0]
		return nil
	})
	return out, err
}

func fulfillmentType(value string) string {
	if value == "" {
		return DeliveryFulfillment
	}
	return value
}
func validateFulfillment(in OrderInput) error {
	switch fulfillmentType(in.FulfillmentType) {
	case DeliveryFulfillment:
		if in.PickupLocationID != "" {
			return apperr.Validation("pickupLocationId is only used for customer pickup orders")
		}
	case CustomerPickupFulfillment:
		if in.TripID != "" {
			return apperr.Validation("Customer pickup orders do not use a delivery trip")
		}
		if strings.TrimSpace(in.PickupLocationID) == "" {
			return apperr.Validation("Choose a collection location for this pickup order")
		}
	default:
		return apperr.Validation("fulfillmentType must be delivery or customerPickup")
	}
	return nil
}
func requireDeliveryOrder(order Order) error {
	if fulfillmentType(order.FulfillmentType) != DeliveryFulfillment {
		return apperr.Conflict("PICKUP_ORDER_REQUIRES_COLLECTION", "Complete customer pickup orders from the order without a delivery trip")
	}
	return nil
}

type CustomerCollection struct {
	Items       []Item    `json:"items" bson:"items"`
	InvoiceID   string    `json:"invoiceId" bson:"invoiceId"`
	PaymentID   string    `json:"paymentId,omitempty" bson:"paymentId,omitempty"`
	CollectedBy string    `json:"collectedBy" bson:"collectedBy"`
	CollectedAt time.Time `json:"collectedAt" bson:"collectedAt"`
	Reason      string    `json:"reason,omitempty" bson:"reason,omitempty"`
}
type CustomerCollectionInput struct {
	Items           []Item  `json:"items"`
	CollectedAmount float64 `json:"collectedAmount"`
	PaymentMethod   string  `json:"paymentMethod"`
	Reason          string  `json:"reason"`
}
type CustomerCollectionResult struct {
	Order     Order      `json:"order"`
	Invoice   Invoice    `json:"invoice"`
	Payment   *Payment   `json:"payment,omitempty"`
	Movements []Movement `json:"movements"`
}

func collectionStatus(order Order, in CustomerCollectionInput) (string, error) {
	if err := validItems(in.Items, false); err != nil {
		return "", err
	}
	if err := validMoney(in.CollectedAmount, true); err != nil {
		return "", err
	}
	if cents(in.CollectedAmount) > 0 && !validMethod(in.PaymentMethod) {
		return "", apperr.Validation("paymentMethod must be cash, upi, or bank when collecting money")
	}
	if err := validText(in.Reason); err != nil {
		return "", err
	}
	status, err := deliveryStatus(order.Items, in.Items)
	if err != nil {
		return "", err
	}
	if status != "delivered" {
		if strings.TrimSpace(in.Reason) == "" {
			return "", apperr.Validation("A reason is required for a partial collection")
		}
		return "partiallyCollected", nil
	}
	return "collected", nil
}
func customerPickupAccess(a Actor, o Order) error {
	if fulfillmentType(o.FulfillmentType) != CustomerPickupFulfillment {
		return apperr.Forbidden()
	}
	if a.Role == DeliveryPartner && o.CreatedBy != a.UserID {
		return apperr.Forbidden()
	}
	return checkPickup(a, o.PickupLocationID)
}
func (s *Store) CollectCustomerPickup(ctx context.Context, a Actor, id string, in CustomerCollectionInput) (CustomerCollectionResult, error) {
	var out CustomerCollectionResult
	err := s.tx(ctx, a, []string{Admin, Manager, WarehouseStaff, DeliveryPartner}, func(sc mongo.SessionContext, a Actor) error {
		out = CustomerCollectionResult{Movements: []Movement{}}
		var order Order
		if err := s.lock(sc, "orders", "orderId", id, &order); err != nil {
			return err
		}
		if err := customerPickupAccess(a, order); err != nil {
			return err
		}
		if order.Collection != nil || order.Status != "open" {
			return apperr.Conflict("ORDER_ALREADY_CLOSED", "This pickup order is already collected or closed")
		}
		if order.TripID != "" || len(order.Picked) > 0 {
			return apperr.Validation("Customer pickup cannot use vehicle stock")
		}
		status, err := collectionStatus(order, in)
		if err != nil {
			return err
		}
		if _, err = s.storage(sc, order.PickupLocationID); err != nil {
			return err
		}
		// Keep FIFO reservations visible until every guarded stock debit succeeds.
		for _, it := range in.Items {
			if err := s.checkOrderReservation(sc, order, it.VariantID, it.Quantity); err != nil {
				return err
			}
		}
		var customer Customer
		if err := s.lockCustomerLedger(sc, order.CustomerID, &customer); err != nil {
			return err
		}
		out.Invoice, err = s.createInvoice(sc, a, "", order, in.Items)
		if err != nil {
			return err
		}
		for _, it := range in.Items {
			m, err := s.move(sc, a, it.VariantID, it.Quantity, order.PickupLocationID, "", "customerPickup", out.Invoice.InvoiceID, "", in.Reason)
			if err != nil {
				return err
			}
			out.Movements = append(out.Movements, m)
		}
		if cents(in.CollectedAmount) > 0 {
			payment, err := s.recordPayment(sc, a, order.CustomerID, "", in.CollectedAmount, in.PaymentMethod)
			if err != nil {
				return err
			}
			out.Payment = &payment
		}
		if err := s.allocateCredit(sc, order.CustomerID); err != nil {
			return err
		}
		collection := &CustomerCollection{Items: in.Items, InvoiceID: out.Invoice.InvoiceID, CollectedBy: a.UserID, CollectedAt: s.now().UTC().Truncate(time.Millisecond), Reason: strings.TrimSpace(in.Reason)}
		if out.Payment != nil {
			collection.PaymentID = out.Payment.PaymentID
		}
		if err := s.update(sc, "orders", "orderId", id, bson.M{"status": status, "collection": collection}); err != nil {
			return err
		}
		order.Status = status
		order.Collection = collection
		order.ReservedItems = []Item{}
		order.AwaitingStockItems = []Item{}
		out.Order = order
		return nil
	})
	return out, err
}
