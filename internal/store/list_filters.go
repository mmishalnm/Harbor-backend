package store

import (
	"context"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"harbor/internal/apperr"
)

type dateBounds struct{ From, Until time.Time }

func parseDateBounds(filters map[string]string) (dateBounds, error) {
	var out dateBounds
	for _, key := range []string{"dateFrom", "dateTo"} {
		if raw := filters[key]; raw != "" {
			at, err := time.ParseInLocation("2006-01-02", raw, tripTimezone)
			if err != nil {
				return out, apperr.Validation(key + " must be a valid YYYY-MM-DD date")
			}
			if key == "dateFrom" {
				out.From = at.UTC()
			} else {
				out.Until = at.AddDate(0, 0, 1).UTC()
			}
		}
	}
	if !out.From.IsZero() && !out.Until.IsZero() && !out.From.Before(out.Until) {
		return out, apperr.Validation("dateFrom must be on or before dateTo")
	}
	return out, nil
}
func (d dateBounds) matches(at time.Time) bool {
	return (d.From.IsZero() || !at.Before(d.From)) && (d.Until.IsZero() || at.Before(d.Until))
}
func (d dateBounds) query() bson.M {
	out := bson.M{}
	if !d.From.IsZero() {
		out["$gte"] = d.From
	}
	if !d.Until.IsZero() {
		out["$lt"] = d.Until
	}
	return out
}
func filterEnum(value, key string, allowed ...string) error {
	if value == "" {
		return nil
	}
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return apperr.Validation("Unsupported " + key + " filter")
}
func (s *Store) applyListFilters(ctx context.Context, a Actor, resource string, filters map[string]string, out bson.M) error {
	switch resource {
	case "trips":
		if err := filterEnum(filters["status"], "status", "planned", "started", "completed", "settled"); err != nil {
			return err
		}
		if v := filters["status"]; v != "" {
			out["status"] = v
		}
		if v := filters["deliveryPartnerId"]; v != "" {
			out["deliveryPartnerId"] = v
		}
	case "invoices":
		if err := filterEnum(filters["status"], "status", "issued", "cancelled"); err != nil {
			return err
		}
		if v := filters["status"]; v != "" {
			out["status"] = v
		}
	case "payments":
		if err := filterEnum(filters["method"], "method", "cash", "upi", "bank"); err != nil {
			return err
		}
		if v := filters["method"]; v != "" {
			out["method"] = v
		}
		if v := filters["receivedBy"]; v != "" {
			out["receivedBy"] = v
		}
	case "stockMovements":
		if err := filterEnum(filters["kind"], "kind", "receive", "transfer", "adjustment", "pickup", "delivery", "return", "customerPickup"); err != nil {
			return err
		}
		if v := filters["kind"]; v != "" {
			out["kind"] = v
		}
		if v := filters["locationId"]; v != "" {
			delete(out, "locationId")
			out["$or"] = bson.A{bson.M{"fromLocationId": v}, bson.M{"toLocationId": v}}
		}
	case "users":
		if err := filterEnum(filters["role"], "role", Admin, Manager, WarehouseStaff, DeliveryPartner); err != nil {
			return err
		}
		if v := filters["role"]; v != "" {
			out["role"] = v
		}
	case "settlements":
		if v := filters["hasDifference"]; v != "" {
			if v != "true" && v != "false" {
				return apperr.Validation("hasDifference must be true or false")
			}
			out["hasDifference"] = v == "true"
		}
	}
	if resource == "orders" || resource == "stocks" {
		return nil
	}
	dates, err := parseDateBounds(filters)
	if err != nil {
		return err
	}
	if query := dates.query(); len(query) > 0 {
		out["createdAt"] = query
	}
	return nil
}

type historyFilters struct {
	Type, Method string
	Dates        dateBounds
}

func parseHistoryFilters(filters map[string]string, partner bool) (historyFilters, error) {
	out := historyFilters{Type: filters["type"], Method: filters["method"]}
	types := []string{"invoice", "payment", "cancelledInvoice"}
	methods := []string{"cash", "upi", "bank"}
	if partner {
		types = []string{"collection", "tripHandover", "cashHandover", "salaryAdjustment"}
		methods = append(methods, "cashHandover", "salaryAdjustment")
	}
	if err := filterEnum(out.Type, "type", types...); err != nil {
		return out, err
	}
	if err := filterEnum(out.Method, "method", methods...); err != nil {
		return out, err
	}
	var err error
	out.Dates, err = parseDateBounds(filters)
	return out, err
}
func filteredLedgerEntries(entries []LedgerEntry, filters historyFilters) []LedgerEntry {
	out := []LedgerEntry{}
	for _, e := range entries {
		if filters.Type != "" && e.Type != filters.Type {
			continue
		}
		if filters.Method != "" && e.Method != filters.Method {
			continue
		}
		if !filters.Dates.matches(e.CreatedAt) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].EntryID > out[j].EntryID
	})
	return out
}
func filteredCashHistory(entries []PartnerCashHistory, filters historyFilters) []PartnerCashHistory {
	out := []PartnerCashHistory{}
	for _, e := range entries {
		if filters.Type != "" && e.Kind != filters.Type {
			continue
		}
		if filters.Method != "" && e.Method != filters.Method {
			continue
		}
		if filters.Dates.matches(e.CreatedAt) {
			out = append(out, e)
		}
	}
	return out
}
