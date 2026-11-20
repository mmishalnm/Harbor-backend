package store

import (
	"context"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"harbor/internal/apperr"
	"harbor/internal/database"
)

type SalesPeriod struct {
	DateFrom string `json:"dateFrom"`
	DateTo   string `json:"dateTo"`
	TimeZone string `json:"timeZone"`
}
type SalesSummary struct {
	SalesAmount         float64 `json:"salesAmount"`
	UnitsSold           int64   `json:"unitsSold"`
	InvoiceCount        int     `json:"invoiceCount"`
	CustomerCount       int     `json:"customerCount"`
	AverageInvoiceValue float64 `json:"averageInvoiceValue"`
}
type SalesCollections struct {
	Total float64 `json:"total"`
	Cash  float64 `json:"cash"`
	UPI   float64 `json:"upi"`
	Bank  float64 `json:"bank"`
}
type SalesVariant struct {
	ProductID        string  `json:"productId"`
	ProductName      string  `json:"productName"`
	VariantID        string  `json:"variantId"`
	VariantName      string  `json:"variantName"`
	UnitsSold        int64   `json:"unitsSold"`
	SalesAmount      float64 `json:"salesAmount"`
	InvoiceCount     int     `json:"invoiceCount"`
	AverageUnitPrice float64 `json:"averageUnitPrice"`
}
type SalesPoint struct {
	Date         string  `json:"date"`
	SalesAmount  float64 `json:"salesAmount"`
	UnitsSold    int64   `json:"unitsSold"`
	InvoiceCount int     `json:"invoiceCount"`
}
type SalesAnalytics struct {
	Period           SalesPeriod        `json:"period"`
	Filters          map[string]string  `json:"filters"`
	GroupBy          string             `json:"groupBy"`
	Summary          SalesSummary       `json:"summary"`
	Collections      *SalesCollections  `json:"collections"`
	CollectionsScope string             `json:"collectionsScope"`
	Products         List[SalesVariant] `json:"products"`
	Trend            List[SalesPoint]   `json:"trend"`
	GeneratedAt      time.Time          `json:"generatedAt"`
}
type salesFilters struct {
	dates                                     dateBounds
	period                                    SalesPeriod
	productID, variantID, customerID, groupBy string
}

func parseSalesFilters(values map[string]string, now time.Time) (salesFilters, error) {
	f := salesFilters{productID: values["productId"], variantID: values["variantId"], customerID: values["customerId"], groupBy: values["groupBy"]}
	for key, prefix := range map[string]string{"productId": "MPN-", "variantId": "MVN-", "customerId": "CUS-"} {
		if id := values[key]; id != "" && (!displayID.MatchString(id) || !strings.HasPrefix(id, prefix) || len(id) > 64) {
			return f, apperr.Validation(key + " must be a valid business ID")
		}
	}
	if f.groupBy == "" {
		f.groupBy = "day"
	}
	if err := filterEnum(f.groupBy, "groupBy", "day", "month"); err != nil {
		return f, err
	}
	from, to := values["dateFrom"], values["dateTo"]
	if to == "" {
		to = now.In(tripTimezone).Format("2006-01-02")
	}
	if from == "" {
		end, err := time.ParseInLocation("2006-01-02", to, tripTimezone)
		if err != nil {
			return f, apperr.Validation("dateTo must be a valid YYYY-MM-DD date")
		}
		from = time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, tripTimezone).Format("2006-01-02")
	}
	dates, err := parseDateBounds(map[string]string{"dateFrom": from, "dateTo": to})
	if err != nil {
		return f, err
	}
	if dates.Until.Sub(dates.From) > 366*24*time.Hour {
		return f, apperr.Validation("Choose a sales period of at most 366 days")
	}
	f.dates = dates
	f.period = SalesPeriod{DateFrom: from, DateTo: to, TimeZone: "Asia/Kolkata"}
	return f, nil
}
func salesBucket(at time.Time, group string) string {
	if group == "month" {
		return at.In(tripTimezone).Format("2006-01") + "-01"
	}
	return at.In(tripTimezone).Format("2006-01-02")
}
func salesAverage(amount int64, count int64) float64 {
	if count == 0 {
		return 0
	}
	return money((amount + count/2) / count)
}
func salesPage[T any](rows []T, p Page) List[T] {
	out := List[T]{Items: []T{}, Page: p.Page, Limit: p.Limit, Total: int64(len(rows))}
	start := (p.Page - 1) * p.Limit
	if start < len(rows) {
		end := start + p.Limit
		if end > len(rows) {
			end = len(rows)
		}
		out.Items = rows[start:end]
	}
	return out
}

type variantSales struct {
	row   SalesVariant
	cents int64
}
type pointSales struct {
	row   SalesPoint
	cents int64
}
type salesAccumulator struct {
	filters       salesFilters
	variants      map[string]Variant
	products      map[string]Product
	amount, units int64
	invoices      int
	customers     map[string]bool
	rows          map[string]*variantSales
	points        map[string]*pointSales
}

func newSalesAccumulator(f salesFilters, variants map[string]Variant, products map[string]Product) *salesAccumulator {
	return &salesAccumulator{filters: f, variants: variants, products: products, customers: map[string]bool{}, rows: map[string]*variantSales{}, points: map[string]*pointSales{}}
}
func (a *salesAccumulator) add(invoice Invoice) {
	if invoice.Status != "issued" || !a.filters.dates.matches(invoice.CreatedAt) || a.filters.customerID != "" && invoice.CustomerID != a.filters.customerID {
		return
	}
	var amount, units int64
	seen := map[string]bool{}
	for _, line := range invoice.Lines {
		variant := a.variants[line.VariantID]
		if a.filters.variantID != "" && line.VariantID != a.filters.variantID || a.filters.productID != "" && variant.ProductID != a.filters.productID {
			continue
		}
		row := a.rows[line.VariantID]
		if row == nil {
			productName := a.products[variant.ProductID].Name
			if productName == "" {
				productName = "Product unavailable"
			}
			name := variant.Name
			if name == "" {
				name = line.Name
			}
			row = &variantSales{row: SalesVariant{ProductID: variant.ProductID, ProductName: productName, VariantID: line.VariantID, VariantName: name}}
			a.rows[line.VariantID] = row
		}
		amount += cents(line.Amount)
		units += line.Quantity
		row.cents += cents(line.Amount)
		row.row.UnitsSold += line.Quantity
		if !seen[line.VariantID] {
			row.row.InvoiceCount++
			seen[line.VariantID] = true
		}
	}
	if len(seen) == 0 {
		return
	}
	a.amount += amount
	a.units += units
	a.invoices++
	a.customers[invoice.CustomerID] = true
	key := salesBucket(invoice.CreatedAt, a.filters.groupBy)
	point := a.points[key]
	if point == nil {
		point = &pointSales{row: SalesPoint{Date: key}}
		a.points[key] = point
	}
	point.cents += amount
	point.row.UnitsSold += units
	point.row.InvoiceCount++
}
func (a *salesAccumulator) result(p, trendPage Page, now time.Time) SalesAnalytics {
	f := a.filters
	out := SalesAnalytics{Period: f.period, Filters: map[string]string{}, GroupBy: f.groupBy, GeneratedAt: now.UTC(), Summary: SalesSummary{SalesAmount: money(a.amount), UnitsSold: a.units, InvoiceCount: a.invoices, CustomerCount: len(a.customers), AverageInvoiceValue: salesAverage(a.amount, int64(a.invoices))}, CollectionsScope: "Customer receipts dated in this period; may settle older invoices or become advance credit. Not sales revenue."}
	for key, value := range map[string]string{"productId": f.productID, "variantId": f.variantID, "customerId": f.customerID} {
		if value != "" {
			out.Filters[key] = value
		}
	}
	rows := []SalesVariant{}
	for _, v := range a.rows {
		r := v.row
		r.SalesAmount = money(v.cents)
		r.AverageUnitPrice = salesAverage(v.cents, r.UnitsSold)
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].SalesAmount != rows[j].SalesAmount {
			return rows[i].SalesAmount > rows[j].SalesAmount
		}
		return rows[i].VariantID < rows[j].VariantID
	})
	out.Products = salesPage(rows, p)
	points := []SalesPoint{}
	start := f.dates.From.In(tripTimezone)
	if f.groupBy == "month" {
		start = time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, tripTimezone)
	}
	for at := start; at.Before(f.dates.Until); {
		key := salesBucket(at, f.groupBy)
		point := SalesPoint{Date: key}
		if v := a.points[key]; v != nil {
			point = v.row
			point.SalesAmount = money(v.cents)
		}
		points = append(points, point)
		if f.groupBy == "month" {
			at = at.AddDate(0, 1, 0)
		} else {
			at = at.AddDate(0, 0, 1)
		}
	}
	out.Trend = salesPage(points, trendPage)
	return out
}

// SalesAnalytics reads a single snapshot and never stores editable totals.
func (s *Store) SalesAnalytics(ctx context.Context, a Actor, values map[string]string, p, trendPage Page) (SalesAnalytics, error) {
	var out SalesAnalytics
	if err := allow(a, Admin, Manager); err != nil {
		return out, err
	}
	f, err := parseSalesFilters(values, s.now())
	if err != nil {
		return out, err
	}
	if err := ValidatePage(p); err != nil {
		return out, err
	}
	if err := ValidatePage(trendPage); err != nil {
		return out, err
	}
	ctx = database.WithAccount(ctx, a.AccountID)
	err = s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		if f.productID != "" {
			var product Product
			if err := s.find(sc, "products", "productId", f.productID, &product); err != nil {
				return err
			}
		}
		if f.variantID != "" {
			var variant Variant
			if err := s.find(sc, "variants", "variantId", f.variantID, &variant); err != nil {
				return err
			}
			if f.productID != "" && variant.ProductID != f.productID {
				return apperr.Validation("Selected variant does not belong to the selected product")
			}
		}
		if f.customerID != "" {
			var customer Customer
			if err := s.find(sc, "customers", "customerId", f.customerID, &customer); err != nil {
				return err
			}
		}
		catalog, err := readAll[Product](sc, s.col("products"), bson.M{}, bson.D{})
		if err != nil {
			return err
		}
		variants, err := readAll[Variant](sc, s.col("variants"), bson.M{}, bson.D{})
		if err != nil {
			return err
		}
		byProduct, byVariant := map[string]Product{}, map[string]Variant{}
		for _, v := range catalog {
			byProduct[v.ProductID] = v
		}
		for _, v := range variants {
			byVariant[v.VariantID] = v
		}
		acc := newSalesAccumulator(f, byVariant, byProduct)
		query := bson.M{"status": "issued", "createdAt": f.dates.query()}
		if f.customerID != "" {
			query["customerId"] = f.customerID
		}
		if f.productID != "" || f.variantID != "" {
			ids := []string{}
			for _, v := range variants {
				if (f.productID == "" || v.ProductID == f.productID) && (f.variantID == "" || v.VariantID == f.variantID) {
					ids = append(ids, v.VariantID)
				}
			}
			query["lines.variantId"] = bson.M{"$in": ids}
		}
		cur, err := s.col("invoices").Find(sc, query, options.Find().SetBatchSize(500))
		if err != nil {
			return err
		}
		for cur.Next(sc) {
			var invoice Invoice
			if err := cur.Decode(&invoice); err != nil {
				_ = cur.Close(sc)
				return err
			}
			acc.add(invoice)
		}
		err = cur.Err()
		_ = cur.Close(sc)
		if err != nil {
			return err
		}
		out = acc.result(p, trendPage, s.now())
		if f.productID != "" || f.variantID != "" {
			out.CollectionsScope = "Customer receipts cannot be attributed to a product or variant. Collections are omitted for product-filtered reports."
			return nil
		}
		receipts := bson.M{"createdAt": f.dates.query()}
		if f.customerID != "" {
			receipts["customerId"] = f.customerID
		}
		cur, err = s.col("payments").Find(sc, receipts, options.Find().SetBatchSize(500))
		if err != nil {
			return err
		}
		var total, cash, upi, bank int64
		for cur.Next(sc) {
			var payment Payment
			if err := cur.Decode(&payment); err != nil {
				_ = cur.Close(sc)
				return err
			}
			amount := cents(payment.Amount)
			total += amount
			switch payment.Method {
			case "cash":
				cash += amount
			case "upi":
				upi += amount
			case "bank":
				bank += amount
			}
		}
		err = cur.Err()
		_ = cur.Close(sc)
		if err != nil {
			return err
		}
		out.Collections = &SalesCollections{Total: money(total), Cash: money(cash), UPI: money(upi), Bank: money(bank)}
		return nil
	})
	return out, err
}
