package api

import (
	"harbor/internal/store"
	"net/http"
	"strings"
)

func subscriptionExempt(path string) bool {
	return path == "/api/v1/auth/me" || path == "/api/v1/account" || strings.HasPrefix(path, "/api/v1/subscription") || strings.HasPrefix(path, "/api/v1/platform/")
}
func (s *Server) registerSubscriptions() {
	s.registerSubscriptionBilling()
	platform := []string{store.SuperAdmin}
	s.add("GET", "/api/v1/platform/businesses", platform, 200, func(a store.Actor, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Store.Businesses(r.Context(), a, p, r.URL.Query().Get("isActive"))
	})
	s.add("PUT", "/api/v1/platform/businesses/{id}/status", platform, 200, body(func(a store.Actor, r *http.Request, in store.BusinessStatusInput) (any, error) {
		return s.Store.SetBusinessStatus(r.Context(), a, r.PathValue("id"), in)
	}))
	s.add("GET", "/api/v1/platform/plans", platform, 200, func(a store.Actor, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Store.Plans(r.Context(), a, p, r.URL.Query().Get("isActive"), true)
	})
	s.add("POST", "/api/v1/platform/plans", platform, 201, body(func(a store.Actor, r *http.Request, in store.PlanInput) (any, error) {
		return s.Store.SavePlan(r.Context(), a, "", in)
	}))
	s.add("PUT", "/api/v1/platform/plans/{id}", platform, 200, body(func(a store.Actor, r *http.Request, in store.PlanInput) (any, error) {
		return s.Store.SavePlan(r.Context(), a, r.PathValue("id"), in)
	}))
	s.add("POST", "/api/v1/platform/plans/{id}/archive", platform, 200, func(a store.Actor, r *http.Request) (any, error) {
		err := s.Store.ArchivePlan(r.Context(), a, r.PathValue("id"))
		return map[string]bool{"isActive": false}, err
	})
	s.add("GET", "/api/v1/subscription", everyone, 200, func(a store.Actor, r *http.Request) (any, error) { return s.Store.SubscriptionStatus(r.Context(), a) })
	s.add("GET", "/api/v1/subscription/plans", []string{store.Admin}, 200, func(a store.Actor, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Store.Plans(r.Context(), a, p, "true", false)
	})
	s.add("POST", "/api/v1/subscription", []string{store.Admin}, 200, body(func(a store.Actor, r *http.Request, in struct {
		PlanID string `json:"planId"`
	}) (any, error) {
		return s.Store.Subscribe(r.Context(), a, in.PlanID)
	}))
	for _, action := range []string{"cancel", "resume"} {
		s.add("POST", "/api/v1/subscription/"+action, []string{store.Admin}, 200, func(a store.Actor, r *http.Request) (any, error) {
			return s.Store.SetSubscriptionRenewal(r.Context(), a, action == "resume")
		})
	}
}
