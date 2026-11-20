package api

import (
	"harbor/internal/store"
	"testing"
	"time"
)

func TestMongoSalesAnalyticsHistoricalPricesFiltersAndTenantScope(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 6}, 201)
	f.deliver(t, f.customer.CustomerID, 2, 0)
	must[store.Variant](t, c, "PATCH", "/api/v1/variants/"+f.variant.VariantID+"/price", map[string]float64{"price": 150}, 200)
	f.deliver(t, f.customer.CustomerID, 1, 0)
	cancelled, _ := f.deliver(t, f.customer.CustomerID, 1, 0)
	must[store.Invoice](t, c, "POST", "/api/v1/invoices/"+cancelled.Invoice.InvoiceID+"/cancel", map[string]string{"reason": "Entered in error"}, 200)
	must[store.Payment](t, c, "POST", "/api/v1/payments", store.PaymentInput{CustomerID: f.customer.CustomerID, Amount: 125, Method: "cash"}, 201)
	other := must[store.Customer](t, c, "POST", "/api/v1/customers", store.CustomerInput{Name: "Other customer"}, 201)
	must[store.Payment](t, c, "POST", "/api/v1/payments", store.PaymentInput{CustomerID: other.CustomerID, Amount: 250, Method: "upi"}, 201)
	now := time.Now().UTC().Add(5*time.Hour + 30*time.Minute)
	start := now.AddDate(0, 0, -1).Format("2006-01-02")
	end := now.AddDate(0, 0, 1).Format("2006-01-02")
	path := "/api/v1/analytics/sales?dateFrom=" + start + "&dateTo=" + end
	report := must[store.SalesAnalytics](t, c, "GET", path, nil, 200)
	if report.Summary.SalesAmount != 390 || report.Summary.UnitsSold != 3 || report.Summary.InvoiceCount != 2 || report.Summary.CustomerCount != 1 || report.Summary.AverageInvoiceValue != 195 {
		t.Fatal(report.Summary)
	}
	if report.Products.Items[0].AverageUnitPrice != 130 || report.Collections == nil || report.Collections.Total != 375 || report.Collections.Cash != 125 || report.Collections.UPI != 250 {
		t.Fatal(report)
	}
	filtered := must[store.SalesAnalytics](t, c, "GET", path+"&productId="+f.variant.ProductID+"&limit=1&trendLimit=1&trendPage=2", nil, 200)
	if filtered.Summary.SalesAmount != 390 || filtered.Collections != nil || filtered.Products.Total != 1 || filtered.Trend.Total != 3 || len(filtered.Trend.Items) != 1 {
		t.Fatal(filtered)
	}
	customer := must[store.SalesAnalytics](t, c, "GET", path+"&customerId="+other.CustomerID, nil, 200)
	if customer.Summary.SalesAmount != 0 || customer.Collections.Total != 250 {
		t.Fatal(customer)
	}
	must[map[string]any](t, c, "POST", "/api/v1/products/"+f.variant.ProductID+"/archive", nil, 200)
	archived := must[store.SalesAnalytics](t, c, "GET", path+"&productId="+f.variant.ProductID, nil, 200)
	if archived.Summary.SalesAmount != 390 || archived.Products.Items[0].ProductName != "ICE" {
		t.Fatal(archived)
	}
	foreign, _ := signupBusiness(t, c, "salesforeign", "Another business")
	private := must[store.SalesAnalytics](t, foreign, "GET", path+"&accountId="+f.driver.AccountID, nil, 200)
	if private.Summary.SalesAmount != 0 || private.Collections.Total != 0 || private.Products.Total != 0 {
		t.Fatal("cross-business sales", private)
	}
	rider := loginAs(t, c, "driver")
	wantError(t, rider, "GET", path, nil, 403, "FORBIDDEN")
	createUser(t, c, "salesstaff", store.WarehouseStaff)
	staff := loginAs(t, c, "salesstaff")
	wantError(t, staff, "GET", path, nil, 403, "FORBIDDEN")
	createUser(t, c, "salesmanager", store.Manager)
	must[store.SalesAnalytics](t, loginAs(t, c, "salesmanager"), "GET", path, nil, 200)
	for _, query := range []string{"&limit=201", "&trendLimit=201", "&trendPage=nope", "&groupBy=year", "&productId=CUS-1"} {
		wantError(t, c, "GET", path+query, nil, 400, "VALIDATION_ERROR")
	}
	wantError(t, c, "GET", "/api/v1/analytics/sales?dateFrom=2020-01-01&dateTo="+end, nil, 400, "VALIDATION_ERROR")
	foreignProduct := must[store.Product](t, foreign, "POST", "/api/v1/products", store.ProductInput{Name: "Foreign"}, 201)
	// Add a second product so the ID cannot also identify this business's product.
	foreignProduct = must[store.Product](t, foreign, "POST", "/api/v1/products", store.ProductInput{Name: "Foreign only"}, 201)
	wantError(t, c, "GET", path+"&productId="+foreignProduct.ProductID, nil, 404, "NOT_FOUND")
	anotherProduct := must[store.Product](t, c, "POST", "/api/v1/products", store.ProductInput{Name: "Notebook"}, 201)
	anotherVariant := must[store.Variant](t, c, "POST", "/api/v1/variants", store.VariantInput{ProductID: anotherProduct.ProductID, Name: "A4", Price: 65}, 201)
	wantError(t, c, "GET", path+"&productId="+f.variant.ProductID+"&variantId="+anotherVariant.VariantID, nil, 400, "VALIDATION_ERROR")
	noSales := must[store.SalesAnalytics](t, c, "GET", path+"&variantId="+anotherVariant.VariantID, nil, 200)
	if noSales.Summary.SalesAmount != 0 || noSales.Products.Total != 0 || noSales.Collections != nil {
		t.Fatal(noSales)
	}

}
