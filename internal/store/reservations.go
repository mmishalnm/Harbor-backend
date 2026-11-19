package store

import (
	"context"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"harbor/internal/apperr"
	"harbor/internal/database"
)

// Reservations are derived from open order lines minus picked quantities. They
// never change physical stock, require no editable balance, and include existing
// orders on upgrade. Earlier orders get first claim on incoming storage stock.
type reservationLine struct{ Reserved, Awaiting int64 }
type reservationPlan struct {
	Orders                                  map[string]map[string]reservationLine
	OnHand, Demand, Reserved, StockReserved map[string]int64
}

func buildReservationPlan(stocks []Stock, orders []Order) reservationPlan {
	p := reservationPlan{Orders: map[string]map[string]reservationLine{}, OnHand: map[string]int64{}, Demand: map[string]int64{}, Reserved: map[string]int64{}, StockReserved: map[string]int64{}}
	stocks = append([]Stock(nil), stocks...)
	orders = append([]Order(nil), orders...)
	sort.Slice(stocks, func(i, j int) bool { return stocks[i].StockKey < stocks[j].StockKey })
	sort.Slice(orders, func(i, j int) bool {
		if !orders[i].CreatedAt.Equal(orders[j].CreatedAt) {
			return orders[i].CreatedAt.Before(orders[j].CreatedAt)
		}
		return orders[i].OrderID < orders[j].OrderID
	})
	for _, row := range stocks {
		p.OnHand[row.VariantID] += row.Quantity
	}
	remaining := map[string]int64{}
	for id, q := range p.OnHand {
		remaining[id] = q
	}
	for _, order := range orders {
		if !order.IsActive || (order.Status != "open" && order.Status != "assigned") {
			continue
		}
		lines := map[string]reservationLine{}
		picked := quantities(order.Picked)
		for _, item := range order.Items {
			q := item.Quantity - picked[item.VariantID]
			if q <= 0 {
				continue
			}
			reserved := min(q, remaining[item.VariantID])
			remaining[item.VariantID] -= reserved
			p.Demand[item.VariantID] += q
			p.Reserved[item.VariantID] += reserved
			lines[item.VariantID] = reservationLine{reserved, q - reserved}
		}
		p.Orders[order.OrderID] = lines
	}
	reserved := map[string]int64{}
	for id, q := range p.Reserved {
		reserved[id] = q
	}
	for _, row := range stocks {
		q := min(row.Quantity, reserved[row.VariantID])
		p.StockReserved[row.StockKey] = q
		reserved[row.VariantID] -= q
	}
	return p
}
func (s *Store) reservationSnapshot(ctx context.Context, variantIDs []string) (reservationPlan, error) {
	variants := bson.M{"$in": variantIDs}
	warehouseIDs, err := s.col("warehouses").Distinct(ctx, "warehouseId", bson.M{"isActive": true})
	if err != nil {
		return reservationPlan{}, err
	}
	locationIDs, err := s.col("locations").Distinct(ctx, "locationId", bson.M{"isActive": true, "warehouseId": bson.M{"$in": warehouseIDs}})
	if err != nil {
		return reservationPlan{}, err
	}
	cur, err := s.col("stocks").Find(ctx, bson.M{"variantId": variants, "locationId": bson.M{"$in": locationIDs}})
	if err != nil {
		return reservationPlan{}, err
	}
	var stocks []Stock
	if err = cur.All(ctx, &stocks); err != nil {
		return reservationPlan{}, err
	}
	cur, err = s.col("orders").Find(ctx, bson.M{"isActive": true, "status": bson.M{"$in": []string{"open", "assigned"}}, "items.variantId": variants})
	if err != nil {
		return reservationPlan{}, err
	}
	var orders []Order
	if err = cur.All(ctx, &orders); err != nil {
		return reservationPlan{}, err
	}
	return buildReservationPlan(stocks, orders), nil
}
func orderVariantIDs(orders []Order) []string {
	seen := map[string]bool{}
	ids := []string{}
	for _, o := range orders {
		for _, it := range o.Items {
			if !seen[it.VariantID] {
				seen[it.VariantID] = true
				ids = append(ids, it.VariantID)
			}
		}
	}
	return ids
}
func applyOrderReservation(order *Order, p reservationPlan) {
	order.ReservedItems = []Item{}
	order.AwaitingStockItems = []Item{}
	for _, it := range order.Items {
		line := p.Orders[order.OrderID][it.VariantID]
		if line.Reserved > 0 {
			order.ReservedItems = append(order.ReservedItems, Item{it.VariantID, line.Reserved})
		}
		if line.Awaiting > 0 {
			order.AwaitingStockItems = append(order.AwaitingStockItems, Item{it.VariantID, line.Awaiting})
		}
	}
}
func (s *Store) enrichOrderReservations(ctx context.Context, orders []Order) error {
	if len(orders) == 0 {
		return nil
	}
	p, err := s.reservationSnapshot(ctx, orderVariantIDs(orders))
	if err != nil {
		return err
	}
	for i := range orders {
		applyOrderReservation(&orders[i], p)
	}
	return nil
}
func (s *Store) reservedOrders(ctx context.Context, filter bson.M, page Page, order bson.D) (List[Order], error) {
	var out List[Order]
	err := s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		var err error
		out, err = list[Order](sc, s, "orders", filter, page, order)
		if err != nil {
			return err
		}
		return s.enrichOrderReservations(sc, out.Items)
	})
	return out, err
}
func (s *Store) reservedStocks(ctx context.Context, filter bson.M, page Page) (List[Stock], error) {
	var out List[Stock]
	err := s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		var err error
		out, err = list[Stock](sc, s, "stocks", filter, page, bson.D{{Key: "stockKey", Value: 1}})
		if err != nil {
			return err
		}
		ids := []string{}
		for _, row := range out.Items {
			ids = append(ids, row.VariantID)
		}
		if len(ids) == 0 {
			return nil
		}
		plan, err := s.reservationSnapshot(sc, ids)
		if err != nil {
			return err
		}
		for i := range out.Items {
			row := &out.Items[i]
			row.ReservedQuantity = plan.StockReserved[row.StockKey]
			row.AvailableQuantity = row.Quantity - row.ReservedQuantity
			if strings.HasPrefix(row.LocationID, "VEH-") {
				row.AvailableQuantity = 0
			}
		}
		return nil
	})
	return out, err
}

type Availability struct {
	VariantID             string `json:"variantId"`
	OnHandQuantity        int64  `json:"onHandQuantity"`
	ReservedQuantity      int64  `json:"reservedQuantity"`
	AvailableQuantity     int64  `json:"availableQuantity"`
	AwaitingStockQuantity int64  `json:"awaitingStockQuantity"`
}
type AvailabilityTotals struct {
	OnHandQuantity        int64 `json:"onHandQuantity"`
	ReservedQuantity      int64 `json:"reservedQuantity"`
	AvailableQuantity     int64 `json:"availableQuantity"`
	AwaitingStockQuantity int64 `json:"awaitingStockQuantity"`
}
type AvailabilityList struct {
	List[Availability]
	Totals AvailabilityTotals `json:"totals"`
}

func (s *Store) InventoryAvailability(ctx context.Context, a Actor, page Page) (AvailabilityList, error) {
	var out AvailabilityList
	if err := allow(a, Admin, Manager); err != nil {
		return out, err
	}
	if err := ValidatePage(page); err != nil {
		return out, err
	}
	ctx = database.WithAccount(ctx, a.AccountID)
	err := s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		out = AvailabilityList{List: List[Availability]{Items: []Availability{}, Page: page.Page, Limit: page.Limit}}
		cur, err := s.col("variants").Find(sc, bson.M{"isActive": true}, options.Find().SetSort(bson.D{{Key: "variantId", Value: 1}}))
		if err != nil {
			return err
		}
		var variants []Variant
		if err = cur.All(sc, &variants); err != nil {
			return err
		}
		ids := []string{}
		for _, v := range variants {
			ids = append(ids, v.VariantID)
		}
		plan, err := s.reservationSnapshot(sc, ids)
		if err != nil {
			return err
		}
		out.Total = int64(len(variants))
		start := (page.Page - 1) * page.Limit
		end := start + page.Limit
		for i, v := range variants {
			row := Availability{v.VariantID, plan.OnHand[v.VariantID], plan.Reserved[v.VariantID], plan.OnHand[v.VariantID] - plan.Reserved[v.VariantID], plan.Demand[v.VariantID] - plan.Reserved[v.VariantID]}
			out.Totals.OnHandQuantity += row.OnHandQuantity
			out.Totals.ReservedQuantity += row.ReservedQuantity
			out.Totals.AvailableQuantity += row.AvailableQuantity
			out.Totals.AwaitingStockQuantity += row.AwaitingStockQuantity
			if i >= start && i < end {
				out.Items = append(out.Items, row)
			}
		}
		return nil
	})
	return out, err
}
func (s *Store) checkOrderReservation(ctx mongo.SessionContext, order Order, variantID string, quantity int64) error {
	plan, err := s.reservationSnapshot(ctx, []string{variantID})
	if err != nil {
		return err
	}
	if plan.Orders[order.OrderID][variantID].Reserved < quantity {
		return apperr.Conflict("INSUFFICIENT_STOCK", "Not enough reserved stock for this order; earlier orders have priority. Receive more stock or release an earlier order")
	}
	return nil
}
