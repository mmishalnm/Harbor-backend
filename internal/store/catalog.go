package store

import (
	"context"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"harbor/internal/apperr"
)

type ProductInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
type VariantInput struct {
	ProductID string  `json:"productId"`
	Name      string  `json:"name"`
	SKU       string  `json:"sku"`
	Price     float64 `json:"price"`
}
type WarehouseInput struct {
	Name string `json:"name"`
}
type LocationInput struct {
	WarehouseID string `json:"warehouseId"`
	Name        string `json:"name"`
	Type        string `json:"type"`
}
type VehicleInput struct {
	Name           string `json:"name"`
	RegistrationNo string `json:"registrationNo"`
}
type CustomerInput struct {
	Name    string `json:"name"`
	Phone   string `json:"phone"`
	Address string `json:"address"`
}

func (s *Store) CreateProduct(ctx context.Context, a Actor, in ProductInput) (Product, error) {
	var out Product
	if err := validName(in.Name); err != nil {
		return out, err
	}
	if err := validText(in.Description); err != nil {
		return out, err
	}
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, _ Actor) error {
		id, err := s.next(sc, "product", "MPN-")
		if err != nil {
			return err
		}
		out = Product{s.base(), id, strings.TrimSpace(in.Name), in.Description}
		return insert(sc, s.col("products"), out)
	})
	return out, err
}
func (s *Store) CreateVariant(ctx context.Context, a Actor, in VariantInput) (Variant, error) {
	var out Variant
	if err := validName(in.Name); err != nil {
		return out, err
	}
	if err := validMoney(in.Price, false); err != nil {
		return out, err
	}
	if len(in.SKU) > 100 {
		return out, apperr.Validation("sku must be at most 100 bytes")
	}
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, _ Actor) error {
		var p Product
		if err := s.lock(sc, "products", "productId", in.ProductID, &p); err != nil {
			return err
		}
		id, err := s.next(sc, "variant", "MVN-")
		if err != nil {
			return err
		}
		out = Variant{s.base(), id, in.ProductID, strings.TrimSpace(in.Name), in.SKU, RoundMoney(in.Price)}
		return insert(sc, s.col("variants"), out)
	})
	return out, err
}
func (s *Store) SetPrice(ctx context.Context, a Actor, id string, price float64) (Variant, error) {
	var out Variant
	if err := validMoney(price, false); err != nil {
		return out, err
	}
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, _ Actor) error {
		if err := s.lock(sc, "variants", "variantId", id, &out); err != nil {
			return err
		}
		out.Price = RoundMoney(price)
		return s.update(sc, "variants", "variantId", id, bson.M{"price": out.Price})
	})
	return out, err
}
func (s *Store) CreateWarehouse(ctx context.Context, a Actor, in WarehouseInput) (Warehouse, error) {
	var out Warehouse
	if err := validName(in.Name); err != nil {
		return out, err
	}
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, _ Actor) error {
		id, err := s.next(sc, "warehouse", "WHS-")
		if err != nil {
			return err
		}
		out = Warehouse{s.base(), id, strings.TrimSpace(in.Name)}
		return insert(sc, s.col("warehouses"), out)
	})
	return out, err
}
func (s *Store) CreateLocation(ctx context.Context, a Actor, in LocationInput) (Location, error) {
	var out Location
	if err := validName(in.Name); err != nil {
		return out, err
	}
	switch in.Type {
	case "freezer", "coldRoom", "storeroom", "rack", "shelf", "other":
	default:
		return out, apperr.Validation("type must be freezer, coldRoom, storeroom, rack, shelf, or other")
	}
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, _ Actor) error {
		var w Warehouse
		if err := s.lock(sc, "warehouses", "warehouseId", in.WarehouseID, &w); err != nil {
			return err
		}
		id, err := s.next(sc, "location", "LOC-")
		if err != nil {
			return err
		}
		out = Location{s.base(), id, in.WarehouseID, strings.TrimSpace(in.Name), in.Type}
		return insert(sc, s.col("locations"), out)
	})
	return out, err
}
func (s *Store) CreateVehicle(ctx context.Context, a Actor, in VehicleInput) (Vehicle, error) {
	var out Vehicle
	if err := validName(in.Name); err != nil {
		return out, err
	}
	if len(in.RegistrationNo) > 100 {
		return out, apperr.Validation("registrationNo must be at most 100 bytes")
	}
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, _ Actor) error {
		id, err := s.next(sc, "vehicle", "VEH-")
		if err != nil {
			return err
		}
		out = Vehicle{s.base(), id, strings.TrimSpace(in.Name), in.RegistrationNo}
		return insert(sc, s.col("vehicles"), out)
	})
	return out, err
}
func (s *Store) CreateCustomer(ctx context.Context, a Actor, in CustomerInput) (Customer, error) {
	var out Customer
	if err := validName(in.Name); err != nil {
		return out, err
	}
	if len(in.Phone) > 30 {
		return out, apperr.Validation("phone must be at most 30 bytes")
	}
	if err := validText(in.Address); err != nil {
		return out, err
	}
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, _ Actor) error {
		id, err := s.next(sc, "customer", "CUS-")
		if err != nil {
			return err
		}
		out = Customer{s.base(), id, strings.TrimSpace(in.Name), in.Phone, in.Address}
		return insert(sc, s.col("customers"), out)
	})
	return out, err
}
