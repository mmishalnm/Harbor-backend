package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"harbor/internal/apperr"
	"harbor/internal/docs"
	"harbor/internal/store"
)

type Route struct {
	Method string
	Path   string
	Roles  []string
}
type Server struct {
	Store  *store.Store
	Logger *slog.Logger
	Routes []Route
	mux    *http.ServeMux
}
type endpoint func(store.Actor, *http.Request) (any, error)

var admins = []string{store.Admin, store.Manager}
var storage = []string{store.Admin, store.Manager, store.WarehouseStaff, store.DeliveryPartner}
var drivers = []string{store.Admin, store.Manager, store.DeliveryPartner}
var everyone = []string{store.Admin, store.Manager, store.WarehouseStaff, store.DeliveryPartner}

func New(s *store.Store, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	a := &Server{Store: s, Logger: logger, mux: http.NewServeMux()}
	a.register()
	return a
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	defer func() {
		if err := recover(); err != nil {
			s.Logger.Error("request panic", "panic", err, "method", r.Method, "path", r.URL.Path)
			s.fail(w, r, errors.New("request panic"))
		}
	}()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	s.mux.ServeHTTP(w, r.WithContext(ctx))
}
func (s *Server) add(method, path string, roles []string, status int, fn endpoint) {
	s.Routes = append(s.Routes, Route{method, path, roles})
	s.mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, r *http.Request) {
		var actor store.Actor
		if len(roles) > 0 {
			parts := strings.Fields(r.Header.Get("Authorization"))
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				s.fail(w, r, apperr.New(401, "UNAUTHORIZED", "Bearer authentication is required"))
				return
			}
			var err error
			actor, err = s.Store.Authenticate(r.Context(), parts[1])
			if err != nil {
				s.fail(w, r, err)
				return
			}
			allowed := false
			for _, role := range roles {
				if actor.Role == role {
					allowed = true
				}
			}
			if !allowed {
				s.fail(w, r, apperr.Forbidden())
				return
			}
			if actor.Role != store.SuperAdmin && !subscriptionExempt(path) {
				if err := s.Store.RequireSubscription(r.Context(), actor); err != nil {
					s.fail(w, r, err)
					return
				}
			}
		}
		value, err := fn(actor, r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		write(w, status, value)
	})
}
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	e := apperr.Public(err)
	if e.Status >= 500 {
		s.Logger.Error("request failed", "error", err, "method", r.Method, "path", r.URL.Path)
	}
	if e.Status == 401 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="harbor"`)
	}
	write(w, e.Status, map[string]any{"error": e})
}
func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func body[T any](fn func(store.Actor, *http.Request, T) (any, error)) endpoint {
	return func(a store.Actor, r *http.Request) (any, error) {
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			return nil, apperr.New(415, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json")
		}
		dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		dec.DisallowUnknownFields()
		var in T
		if err := dec.Decode(&in); err != nil {
			return nil, apperr.Validation("Request body must be valid JSON with supported fields and types")
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return nil, apperr.Validation("Request body must contain exactly one JSON object")
		}
		return fn(a, r, in)
	}
}
func historyQuery(r *http.Request) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"type", "method", "dateFrom", "dateTo"} {
		out[key] = r.URL.Query().Get(key)
	}
	return out
}
func pagination(r *http.Request) (store.Page, error) {
	p := store.Page{Page: 1, Limit: 20}
	for k, ptr := range map[string]*int{"page": &p.Page, "limit": &p.Limit} {
		if v := r.URL.Query().Get(k); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return p, apperr.Validation("page and limit must be integers")
			}
			*ptr = n
		}
	}
	return p, store.ValidatePage(p)
}
func (s *Server) register() {
	s.registerSubscriptions()
	s.add("POST", "/api/v1/coupons", admins, 201, body(func(a store.Actor, r *http.Request, in store.CouponInput) (any, error) {
		return s.Store.SaveCoupon(r.Context(), a, "", in)
	}))
	s.add("PUT", "/api/v1/coupons/{id}", admins, 200, body(func(a store.Actor, r *http.Request, in store.CouponInput) (any, error) {
		return s.Store.SaveCoupon(r.Context(), a, r.PathValue("id"), in)
	}))
	for _, optionsOnly := range []bool{false, true} {
		path, roles := "/api/v1/coupons", admins
		if optionsOnly {
			path += "/options"
			roles = everyone
		}
		s.add("GET", path, roles, 200, func(a store.Actor, r *http.Request) (any, error) {
			p, err := pagination(r)
			if err != nil {
				return nil, err
			}
			return s.Store.Coupons(r.Context(), a, p, r.URL.Query().Get("isActive"), optionsOnly)
		})
	}
	s.add("GET", "/health", nil, 200, func(_ store.Actor, _ *http.Request) (any, error) { return map[string]string{"status": "ok"}, nil })
	s.add("POST", "/api/v1/auth/login", nil, 200, body(func(_ store.Actor, r *http.Request, in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}) (any, error) {
		return s.Store.Login(r.Context(), in.Username, in.Password)
	}))
	s.add("GET", "/api/v1/account/order-settings", everyone, 200, func(a store.Actor, r *http.Request) (any, error) {
		profile, err := s.Store.MyProfile(r.Context(), a)
		return profile.Account.OrderSettings, err
	})
	s.add("PUT", "/api/v1/account/order-settings", []string{store.Admin}, 200, body(func(a store.Actor, r *http.Request, in store.OrderSettingsInput) (any, error) {
		return s.Store.SetOrderSettings(r.Context(), a, in)
	}))
	s.add("PUT", "/api/v1/users/{id}/delivery-vehicle", []string{store.Admin}, 200, body(func(a store.Actor, r *http.Request, in store.DeliveryVehicleInput) (any, error) {
		return s.Store.AssignDeliveryVehicle(r.Context(), a, r.PathValue("id"), in)
	}))
	s.add("POST", "/api/v1/auth/signup", nil, 201, body(func(_ store.Actor, r *http.Request, in store.SignupInput) (any, error) {
		return s.Store.Signup(r.Context(), in)
	}))
	s.add("POST", "/api/v1/display-names", everyone, 200, body(func(a store.Actor, r *http.Request, in store.DisplayNamesInput) (any, error) {
		return s.Store.ResolveDisplayNames(r.Context(), a, in)
	}))
	s.add("GET", "/api/v1/auth/me", append(append([]string{}, everyone...), store.SuperAdmin), 200, func(a store.Actor, r *http.Request) (any, error) { return s.Store.MyProfile(r.Context(), a) })
	s.add("GET", "/api/v1/account", everyone, 200, func(a store.Actor, r *http.Request) (any, error) {
		profile, err := s.Store.MyProfile(r.Context(), a)
		return profile.Account, err
	})
	s.add("PUT", "/api/v1/account", []string{store.Admin}, 200, body(func(a store.Actor, r *http.Request, in store.BusinessProfileInput) (any, error) {
		return s.Store.UpdateBusiness(r.Context(), a, in)
	}))
	s.add("PUT", "/api/v1/trips/{id}/staff", admins, 200, body(func(a store.Actor, r *http.Request, in struct {
		WarehouseStaffIDs []string `json:"warehouseStaffIds"`
	}) (any, error) {
		return s.Store.AssignTripStaff(r.Context(), a, r.PathValue("id"), in.WarehouseStaffIDs)
	}))
	s.add("GET", "/api/v1/analytics/sales", admins, 200, func(a store.Actor, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		trend := store.Page{Page: 1, Limit: 31}
		for key, ptr := range map[string]*int{"trendPage": &trend.Page, "trendLimit": &trend.Limit} {
			if value := r.URL.Query().Get(key); value != "" {
				n, err := strconv.Atoi(value)
				if err != nil {
					return nil, apperr.Validation("trendPage and trendLimit must be integers")
				}
				*ptr = n
			}
		}
		filters := map[string]string{}
		for _, key := range []string{"dateFrom", "dateTo", "productId", "variantId", "customerId", "groupBy"} {
			filters[key] = r.URL.Query().Get(key)
		}
		return s.Store.SalesAnalytics(r.Context(), a, filters, p, trend)
	})
	s.add("POST", "/api/v1/products", admins, 201, body(func(a store.Actor, r *http.Request, in store.ProductInput) (any, error) {
		return s.Store.CreateProduct(r.Context(), a, in)
	}))
	s.add("POST", "/api/v1/variants", admins, 201, body(func(a store.Actor, r *http.Request, in store.VariantInput) (any, error) {
		return s.Store.CreateVariant(r.Context(), a, in)
	}))
	s.add("PATCH", "/api/v1/variants/{id}/price", admins, 200, body(func(a store.Actor, r *http.Request, in struct {
		Price *float64 `json:"price"`
	}) (any, error) {
		if in.Price == nil {
			return nil, apperr.Validation("price is required")
		}
		return s.Store.SetPrice(r.Context(), a, r.PathValue("id"), *in.Price)
	}))
	s.add("POST", "/api/v1/warehouses", admins, 201, body(func(a store.Actor, r *http.Request, in store.WarehouseInput) (any, error) {
		return s.Store.CreateWarehouse(r.Context(), a, in)
	}))
	s.add("POST", "/api/v1/locations", admins, 201, body(func(a store.Actor, r *http.Request, in store.LocationInput) (any, error) {
		return s.Store.CreateLocation(r.Context(), a, in)
	}))
	s.add("POST", "/api/v1/vehicles", admins, 201, body(func(a store.Actor, r *http.Request, in store.VehicleInput) (any, error) {
		return s.Store.CreateVehicle(r.Context(), a, in)
	}))
	s.add("POST", "/api/v1/customers", admins, 201, body(func(a store.Actor, r *http.Request, in store.CustomerInput) (any, error) {
		return s.Store.CreateCustomer(r.Context(), a, in)
	}))
	s.add("POST", "/api/v1/users", []string{store.Admin}, 201, body(func(a store.Actor, r *http.Request, in store.CreateUserInput) (any, error) {
		return s.Store.CreateUser(r.Context(), a, in)
	}))
	s.add("PUT", "/api/v1/users/{id}/locations", admins, 200, body(func(a store.Actor, r *http.Request, in struct {
		LocationIDs []string `json:"locationIds"`
	}) (any, error) {
		return s.Store.AssignLocations(r.Context(), a, r.PathValue("id"), in.LocationIDs)
	}))
	s.add("POST", "/api/v1/stock/receive", storage, 201, body(func(a store.Actor, r *http.Request, in store.ReceiveInput) (any, error) {
		return s.Store.Receive(r.Context(), a, in)
	}))
	s.add("POST", "/api/v1/stock/transfer", storage, 201, body(func(a store.Actor, r *http.Request, in store.TransferInput) (any, error) {
		return s.Store.Transfer(r.Context(), a, in)
	}))
	s.add("POST", "/api/v1/stock/adjust", drivers, 201, body(func(a store.Actor, r *http.Request, in store.AdjustmentInput) (any, error) {
		return s.Store.Adjust(r.Context(), a, in)
	}))
	s.add("GET", "/api/v1/customers/options", drivers, 200, func(a store.Actor, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Store.CustomerOptions(r.Context(), a, p)
	})
	s.add("POST", "/api/v1/orders/{id}/assign", admins, 200, body(func(a store.Actor, r *http.Request, in store.AssignOrderInput) (any, error) {
		return s.Store.AssignOrder(r.Context(), a, r.PathValue("id"), in)
	}))
	s.add("GET", "/api/v1/trips/{id}/cash-summary", admins, 200, func(a store.Actor, r *http.Request) (any, error) {
		return s.Store.TripCash(r.Context(), a, r.PathValue("id"))
	})
	s.add("GET", "/api/v1/partners/cash", admins, 200, func(a store.Actor, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Store.PartnerCashList(r.Context(), a, p)
	})
	s.add("GET", "/api/v1/partners/{id}/cash", drivers, 200, func(a store.Actor, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Store.PartnerCash(r.Context(), a, r.PathValue("id"), p, historyQuery(r))
	})
	s.add("POST", "/api/v1/partners/{id}/cash-settlements", admins, 201, body(func(a store.Actor, r *http.Request, in store.PartnerCashInput) (any, error) {
		return s.Store.SettlePartnerCash(r.Context(), a, r.PathValue("id"), in)
	}))
	s.add("GET", "/api/v1/inventory/availability", admins, 200, func(a store.Actor, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Store.InventoryAvailability(r.Context(), a, p)
	})
	s.add("POST", "/api/v1/orders/bulk-assign", drivers, 200, body(func(a store.Actor, r *http.Request, in store.BulkAssignOrdersInput) (any, error) {
		return s.Store.BulkAssignOrders(r.Context(), a, in)
	}))
	s.add("POST", "/api/v1/orders/bulk-cancel", admins, 200, body(func(a store.Actor, r *http.Request, in store.BulkCancelOrdersInput) (any, error) {
		return s.Store.BulkCancelOrders(r.Context(), a, in)
	}))
	s.add("POST", "/api/v1/orders", everyone, 201, body(func(a store.Actor, r *http.Request, in store.OrderInput) (any, error) {
		return s.Store.CreateOrder(r.Context(), a, in)
	}))
	s.add("POST", "/api/v1/trips", drivers, 201, body(func(a store.Actor, r *http.Request, in store.TripInput) (any, error) {
		return s.Store.CreateTrip(r.Context(), a, in)
	}))
	s.add("GET", "/api/v1/trips/{id}", everyone, 200, func(a store.Actor, r *http.Request) (any, error) {
		return s.Store.GetTrip(r.Context(), a, r.PathValue("id"))
	})
	s.add("GET", "/api/v1/trips/{id}/orders", everyone, 200, func(a store.Actor, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Store.TripOrders(r.Context(), a, r.PathValue("id"), p)
	})
	s.add("GET", "/api/v1/trips/{id}/stock", everyone, 200, func(a store.Actor, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Store.TripStock(r.Context(), a, r.PathValue("id"), p)
	})
	s.add("POST", "/api/v1/trips/{id}/pickup", everyone, 200, body(func(a store.Actor, r *http.Request, in store.PickupInput) (any, error) {
		return s.Store.Pickup(r.Context(), a, r.PathValue("id"), in)
	}))
	s.add("POST", "/api/v1/trips/{id}/start", drivers, 200, func(a store.Actor, r *http.Request) (any, error) {
		return s.Store.StartTrip(r.Context(), a, r.PathValue("id"))
	})
	s.add("POST", "/api/v1/trips/{id}/stops/{orderId}/close", drivers, 200, body(func(a store.Actor, r *http.Request, in store.CloseStopInput) (any, error) {
		return s.Store.CloseStop(r.Context(), a, r.PathValue("id"), r.PathValue("orderId"), in)
	}))
	s.add("POST", "/api/v1/trips/{id}/returns", everyone, 200, body(func(a store.Actor, r *http.Request, in store.ReturnInput) (any, error) {
		return s.Store.ReturnStock(r.Context(), a, r.PathValue("id"), in)
	}))
	s.add("POST", "/api/v1/trips/{id}/complete", drivers, 200, func(a store.Actor, r *http.Request) (any, error) {
		return s.Store.CompleteTrip(r.Context(), a, r.PathValue("id"))
	})
	s.add("POST", "/api/v1/trips/{id}/settle", admins, 201, body(func(a store.Actor, r *http.Request, in store.SettlementInput) (any, error) {
		return s.Store.Settle(r.Context(), a, r.PathValue("id"), in)
	}))
	s.add("POST", "/api/v1/payments", drivers, 201, body(func(a store.Actor, r *http.Request, in store.PaymentInput) (any, error) {
		return s.Store.CollectPayment(r.Context(), a, in)
	}))
	s.add("GET", "/api/v1/customers/{id}/ledger/export", admins, 200, func(a store.Actor, r *http.Request) (any, error) {
		return s.Store.ExportCustomerLedger(r.Context(), a, r.PathValue("id"), "", historyQuery(r))
	})
	s.add("GET", "/api/v1/trips/{id}/customers/{customerId}/ledger/export", drivers, 200, func(a store.Actor, r *http.Request) (any, error) {
		return s.Store.ExportCustomerLedger(r.Context(), a, r.PathValue("customerId"), r.PathValue("id"), historyQuery(r))
	})
	s.add("GET", "/api/v1/customers/{id}/ledger", admins, 200, func(a store.Actor, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Store.CustomerLedger(r.Context(), a, r.PathValue("id"), "", p, historyQuery(r))
	})
	s.add("GET", "/api/v1/trips/{id}/customers/{customerId}/ledger", drivers, 200, func(a store.Actor, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Store.CustomerLedger(r.Context(), a, r.PathValue("customerId"), r.PathValue("id"), p, historyQuery(r))
	})
	s.add("GET", "/api/v1/invoices/{id}", drivers, 200, func(a store.Actor, r *http.Request) (any, error) {
		return s.Store.GetInvoice(r.Context(), a, r.PathValue("id"))
	})
	s.add("POST", "/api/v1/invoices/{id}/cancel", admins, 200, body(func(a store.Actor, r *http.Request, in struct {
		Reason string `json:"reason"`
	}) (any, error) {
		return s.Store.CancelInvoice(r.Context(), a, r.PathValue("id"), in.Reason)
	}))
	for _, resource := range []string{"products", "variants", "warehouses", "locations", "vehicles", "customers", "users", "orders", "trips", "stocks", "stockMovements", "invoices", "payments", "paymentAllocations", "settlements"} {
		roles := admins
		switch resource {
		case "products", "variants", "warehouses", "locations", "vehicles", "trips":
			roles = everyone
		case "orders":
			roles = everyone
		case "stocks":
			roles = storage
		}
		s.add("GET", "/api/v1/"+resource, roles, 200, func(a store.Actor, r *http.Request) (any, error) {
			p, err := pagination(r)
			if err != nil {
				return nil, err
			}
			f := map[string]string{}
			for _, key := range []string{"warehouseId", "productId", "locationId", "variantId", "customerId", "tripId", "isActive", "status", "source", "deliveryPartnerId", "dateFrom", "dateTo", "method", "kind", "role", "vehicleId", "receivedBy", "hasDifference"} {
				f[key] = r.URL.Query().Get(key)
			}
			return s.Store.ListResources(r.Context(), a, resource, p, f)
		})
	}
	for _, resource := range []string{"products", "variants", "customers", "users", "coupons"} {
		roles := admins
		if resource == "users" {
			roles = []string{store.Admin}
		}
		s.add("POST", "/api/v1/"+resource+"/{id}/archive", roles, 200, func(a store.Actor, r *http.Request) (any, error) {
			err := s.Store.Archive(r.Context(), a, resource, r.PathValue("id"))
			return map[string]bool{"isActive": false}, err
		})
	}
	// Public documentation is embedded so working directory does not matter.
	for _, path := range []string{"/docs", "/docs/{$}"} {
		s.mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(docs.UI)
		})
	}
	s.mux.Handle("GET /docs/ui/", http.StripPrefix("/docs/", http.FileServer(http.FS(docs.Assets))))
	s.mux.HandleFunc("GET /docs/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(docs.OpenAPI)
	})
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { s.fail(w, r, apperr.NotFound()) })
}
