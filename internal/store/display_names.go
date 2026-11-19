package store

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"harbor/internal/apperr"
	"harbor/internal/database"
)

type DisplayNamesInput struct {
	IDs []string `json:"ids"`
}
type DisplayNamesResult struct {
	Names map[string]string `json:"names"`
}

var displayID = regexp.MustCompile(`^(MPN|MVN|WHS|LOC|VEH|CUS|USR|ORD|TRP|INV|PAY|MOV)-[0-9]+$`)
var displayCollections = map[string]string{"MPN": "products", "MVN": "variants", "WHS": "warehouses", "LOC": "locations", "VEH": "vehicles", "CUS": "customers", "USR": "users", "ORD": "orders", "TRP": "trips", "INV": "invoices", "PAY": "payments", "MOV": "stockMovements"}

func validateDisplayNames(in DisplayNamesInput) error {
	if len(in.IDs) > 200 {
		return apperr.Validation("Resolve at most 200 references at a time")
	}
	for _, id := range in.IDs {
		if len(id) > 64 || !displayID.MatchString(id) {
			return apperr.Validation("Each reference must be a supported business ID")
		}
	}
	return nil
}

// ResolveDisplayNames exposes labels only. It never expands business, assignment,
// customer-ledger or invoice permissions. Missing and inaccessible IDs are omitted.
func (s *Store) ResolveDisplayNames(ctx context.Context, a Actor, in DisplayNamesInput) (DisplayNamesResult, error) {
	out := DisplayNamesResult{Names: map[string]string{}}
	if err := allow(a, Admin, Manager, WarehouseStaff, DeliveryPartner); err != nil {
		return out, err
	}
	if err := validateDisplayNames(in); err != nil {
		return out, err
	}
	if len(in.IDs) == 0 {
		return out, nil
	}
	ctx = database.WithAccount(ctx, a.AccountID)
	groups := map[string][]string{}
	for _, id := range in.IDs {
		col := displayCollections[strings.SplitN(id, "-", 2)[0]]
		groups[col] = append(groups[col], id)
	}
	office := a.Role == Admin || a.Role == Manager
	// Restricted roles may see customer/team labels only through visible orders
	// and trips, plus their own identity. No customer contacts or balances are read.
	allowedCustomers, allowedUsers := []any{}, []any{a.UserID}
	if !office && (len(groups["customers"])+len(groups["users"]) > 0) {
		filter := bson.M{}
		if err := s.restrictAssignedLists(ctx, a, "orders", filter); err != nil {
			return out, err
		}
		ids, err := s.col("orders").Distinct(ctx, "customerId", filter)
		if err != nil {
			return out, err
		}
		allowedCustomers = ids
		ids, err = s.col("orders").Distinct(ctx, "createdBy", filter)
		if err != nil {
			return out, err
		}
		allowedUsers = append(allowedUsers, ids...)
		filter = bson.M{}
		if err = s.restrictAssignedLists(ctx, a, "trips", filter); err != nil {
			return out, err
		}
		if a.Role == DeliveryPartner {
			filter["deliveryPartnerId"] = a.UserID
		}
		for _, field := range []string{"deliveryPartnerId", "warehouseStaffIds"} {
			ids, err = s.col("trips").Distinct(ctx, field, filter)
			if err != nil {
				return out, err
			}
			allowedUsers = append(allowedUsers, ids...)
		}
	}
	type labelRecord struct {
		Name        string    `bson:"name"`
		ProductID   string    `bson:"productId"`
		VariantID   string    `bson:"variantId"`
		WarehouseID string    `bson:"warehouseId"`
		LocationID  string    `bson:"locationId"`
		VehicleID   string    `bson:"vehicleId"`
		CustomerID  string    `bson:"customerId"`
		UserID      string    `bson:"userId"`
		OrderID     string    `bson:"orderId"`
		TripID      string    `bson:"tripId"`
		InvoiceID   string    `bson:"invoiceId"`
		InvoiceNo   string    `bson:"invoiceNo"`
		PaymentID   string    `bson:"paymentId"`
		MovementID  string    `bson:"movementId"`
		CreatedAt   time.Time `bson:"createdAt"`
		TripDate    string    `bson:"tripDate"`
	}
	// Nested label reads inherit the same account. Their IDs come only from an
	// accessible primary record; no arbitrary parent/customer traversal is allowed.
	nameOf := func(col, field, id string) (string, error) {
		if id == "" {
			return "", nil
		}
		var r struct {
			Name string `bson:"name"`
		}
		err := s.col(col).FindOne(ctx, bson.M{field: id}, options.FindOne().SetProjection(bson.M{"name": 1})).Decode(&r)
		if err != nil {
			return "", err
		}
		return r.Name, nil
	}
	for col, ids := range groups {
		if !office && ((col == "invoices" && a.Role != DeliveryPartner) || col == "payments" || col == "stockMovements") {
			continue
		}
		field := database.BusinessIDs[col]
		requested := bson.M{field: bson.M{"$in": ids}}
		access := bson.M{}
		if err := s.restrictAssignedLists(ctx, a, col, access); err != nil {
			return out, err
		}
		if !office && col == "customers" {
			access["customerId"] = bson.M{"$in": allowedCustomers}
		}
		if !office && col == "users" {
			access["userId"] = bson.M{"$in": allowedUsers}
		}
		if a.Role == DeliveryPartner && (col == "trips" || col == "invoices") {
			tripFilter := bson.M{"deliveryPartnerId": a.UserID}
			if col == "trips" {
				access = tripFilter
			} else {
				tripIDs, err := s.col("trips").Distinct(ctx, "tripId", tripFilter)
				if err != nil {
					return out, err
				}
				access["tripId"] = bson.M{"$in": tripIDs}
			}
		}
		cur, err := s.col(col).Find(ctx, bson.M{"$and": bson.A{requested, access}}, options.Find().SetProjection(bson.M{field: 1, "name": 1, "productId": 1, "warehouseId": 1, "vehicleId": 1, "customerId": 1, "invoiceNo": 1, "tripDate": 1, "createdAt": 1}))
		if err != nil {
			return out, err
		}
		var rows []labelRecord
		if err = cur.All(ctx, &rows); err != nil {
			return out, err
		}
		for _, r := range rows {
			var id, label string
			switch col {
			case "products":
				id, label = r.ProductID, r.Name
			case "warehouses":
				id, label = r.WarehouseID, r.Name
			case "vehicles":
				id, label = r.VehicleID, r.Name
			case "customers":
				id, label = r.CustomerID, r.Name
			case "users":
				id, label = r.UserID, r.Name
			case "variants":
				parent, err := nameOf("products", "productId", r.ProductID)
				if err != nil {
					return out, err
				}
				id, label = r.VariantID, strings.Trim(parent+" · "+r.Name, " ·")
			case "locations":
				parent, err := nameOf("warehouses", "warehouseId", r.WarehouseID)
				if err != nil {
					return out, err
				}
				id, label = r.LocationID, strings.Trim(r.Name+" · "+parent, " ·")
			case "orders", "payments":
				customer, err := nameOf("customers", "customerId", r.CustomerID)
				if err != nil {
					return out, err
				}
				kind := "Order"
				id = r.OrderID
				if col == "payments" {
					kind = "Payment"
					id = r.PaymentID
				}
				label = fmt.Sprintf("%s · %s · %s", customer, kind, r.CreatedAt.In(tripTimezone).Format("02 Jan 2006"))
			case "trips":
				vehicle, err := nameOf("vehicles", "vehicleId", r.VehicleID)
				if err != nil {
					return out, err
				}
				id, label = r.TripID, vehicle+" · "+r.TripDate
			case "invoices":
				id, label = r.InvoiceID, r.InvoiceNo
			case "stockMovements":
				id, label = r.MovementID, "Stock movement · "+r.CreatedAt.In(tripTimezone).Format("02 Jan 2006")
			}
			if id != "" && label != "" {
				out.Names[id] = label
			}
		}
	}
	return out, nil
}
