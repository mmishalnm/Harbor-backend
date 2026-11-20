package api

import (
	"harbor/internal/store"
	"net/http"
)

func (s *Server) registerSubscriptionBilling() {
	platform := []string{store.SuperAdmin}
	s.add("GET", "/api/v1/platform/businesses/{id}/subscription", platform, 200, func(a store.Actor, r *http.Request) (any, error) {
		return s.Store.PlatformSubscription(r.Context(), a, r.PathValue("id"))
	})
	s.add("POST", "/api/v1/platform/businesses/{id}/subscription/bill", platform, 200, func(a store.Actor, r *http.Request) (any, error) {
		return s.Store.PrepareSubscriptionBill(r.Context(), a, r.PathValue("id"))
	})
	s.add("POST", "/api/v1/platform/businesses/{id}/subscription/payments", platform, 201, body(func(a store.Actor, r *http.Request, in store.SubscriptionPaymentInput) (any, error) {
		return s.Store.RecordSubscriptionPayment(r.Context(), a, r.PathValue("id"), in)
	}))
	s.add("GET", "/api/v1/platform/businesses/{id}/subscription/payments", platform, 200, func(a store.Actor, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Store.SubscriptionPayments(r.Context(), a, r.PathValue("id"), p, true)
	})
	s.add("GET", "/api/v1/subscription/payments", []string{store.Admin}, 200, func(a store.Actor, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Store.SubscriptionPayments(r.Context(), a, "", p, false)
	})
	s.add("POST", "/api/v1/subscription/coupon", []string{store.Admin}, 200, body(func(a store.Actor, r *http.Request, in struct {
		Code string `json:"code"`
	}) (any, error) {
		return s.Store.SetSubscriptionCoupon(r.Context(), a, in.Code)
	}))
	s.add("GET", "/api/v1/platform/subscription-coupons", platform, 200, func(a store.Actor, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Store.SubscriptionCoupons(r.Context(), a, p, r.URL.Query().Get("isActive"))
	})
	s.add("POST", "/api/v1/platform/subscription-coupons", platform, 201, body(func(a store.Actor, r *http.Request, in store.CouponInput) (any, error) {
		return s.Store.SaveSubscriptionCoupon(r.Context(), a, "", in)
	}))
	s.add("PUT", "/api/v1/platform/subscription-coupons/{id}", platform, 200, body(func(a store.Actor, r *http.Request, in store.CouponInput) (any, error) {
		return s.Store.SaveSubscriptionCoupon(r.Context(), a, r.PathValue("id"), in)
	}))
	s.add("POST", "/api/v1/platform/subscription-coupons/{id}/archive", platform, 200, func(a store.Actor, r *http.Request) (any, error) {
		err := s.Store.ArchiveSubscriptionCoupon(r.Context(), a, r.PathValue("id"))
		return map[string]bool{"isActive": false}, err
	})
}
