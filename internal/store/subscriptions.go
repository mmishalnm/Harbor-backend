package store

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"harbor/internal/apperr"
	"harbor/internal/database"
)

type Subscription struct {
	SubscriptionID string     `json:"subscriptionId" bson:"subscriptionId"`
	Plan           Plan       `json:"plan" bson:"plan"`
	StartedAt      time.Time  `json:"startedAt" bson:"startedAt"`
	PeriodAnchor   time.Time  `json:"periodAnchor" bson:"periodAnchor"`
	TrialEndsAt    *time.Time `json:"trialEndsAt,omitempty" bson:"trialEndsAt,omitempty"`
	AutoRenew      bool       `json:"autoRenew" bson:"autoRenew"`
	AccessEndsAt   *time.Time `json:"accessEndsAt,omitempty" bson:"accessEndsAt,omitempty"`
}
type PlanUsage struct {
	SKUs       int64 `json:"skus"`
	Warehouses int64 `json:"warehouses"`
	Partners   int64 `json:"partners"`
}
type SubscriptionAccess struct {
	Status         string     `json:"status"`
	HasAccess      bool       `json:"hasAccess"`
	SubscriptionID string     `json:"subscriptionId,omitempty"`
	Plan           *Plan      `json:"plan,omitempty"`
	TrialEndsAt    *time.Time `json:"trialEndsAt,omitempty"`
	PeriodStart    *time.Time `json:"periodStart,omitempty"`
	PeriodEnd      *time.Time `json:"periodEnd,omitempty"`
	NextRenewalAt  *time.Time `json:"nextRenewalAt,omitempty"`
	AutoRenew      bool       `json:"autoRenew"`
	Usage          *PlanUsage `json:"usage,omitempty"`
}
type SubscriptionEvent struct {
	EventID      string       `json:"eventId" bson:"eventId"`
	Kind         string       `json:"kind" bson:"kind"`
	ActorID      string       `json:"actorId" bson:"actorId"`
	Subscription Subscription `json:"subscription" bson:"subscription"`
	CreatedAt    time.Time    `json:"createdAt" bson:"createdAt"`
}

var billingZone = time.FixedZone("IST", 5*3600+30*60)

// Boundaries retain the original Indian calendar billing day (31 Jan -> 28 Feb
// -> 31 Mar), rather than drifting after a short month. No background job or
// fake payment is needed to renew a zero-price accepted plan.
func billingBoundary(anchor time.Time, months int) time.Time {
	a := anchor.In(billingZone)
	target := time.Date(a.Year(), a.Month()+time.Month(months), 1, a.Hour(), a.Minute(), a.Second(), a.Nanosecond(), billingZone)
	last := time.Date(target.Year(), target.Month()+1, 0, 0, 0, 0, 0, billingZone).Day()
	day := a.Day()
	if day > last {
		day = last
	}
	return time.Date(target.Year(), target.Month(), day, a.Hour(), a.Minute(), a.Second(), a.Nanosecond(), billingZone).UTC()
}
func billingPeriod(anchor, now time.Time, interval string) (time.Time, time.Time) {
	step := 1
	if interval == "yearly" {
		step = 12
	}
	a, n := anchor.In(billingZone), now.In(billingZone)
	count := ((n.Year()-a.Year())*12 + int(n.Month()-a.Month())) / step
	if count < 0 {
		count = 0
	}
	start := billingBoundary(anchor, count*step)
	if start.After(now) && count > 0 {
		count--
		start = billingBoundary(anchor, count*step)
	}
	end := billingBoundary(anchor, (count+1)*step)
	return start, end
}
func subscriptionAccess(account BusinessAccount, now time.Time) SubscriptionAccess {
	out := SubscriptionAccess{Status: "required"}
	sub := account.Subscription
	if sub != nil {
		out.SubscriptionID = sub.SubscriptionID
		out.Plan = &sub.Plan
		out.TrialEndsAt = sub.TrialEndsAt
		out.AutoRenew = sub.AutoRenew
		periodTime := now
		if !sub.AutoRenew && sub.AccessEndsAt != nil && !now.Before(*sub.AccessEndsAt) {
			periodTime = sub.AccessEndsAt.Add(-time.Nanosecond)
		}
		start, end := billingPeriod(sub.PeriodAnchor, periodTime, sub.Plan.BillingInterval)
		if sub.TrialEndsAt != nil && periodTime.Before(*sub.TrialEndsAt) {
			start = sub.StartedAt
			end = *sub.TrialEndsAt
		}
		out.PeriodStart = &start
		out.PeriodEnd = &end
		if !sub.AutoRenew && sub.AccessEndsAt != nil && !now.Before(*sub.AccessEndsAt) {
			out.Status = "expired"
			out.PeriodEnd = sub.AccessEndsAt
		} else if sub.TrialEndsAt != nil && now.Before(*sub.TrialEndsAt) {
			out.Status = "trialing"
			out.HasAccess = true
		} else if sub.Plan.Price == 0 {
			out.Status = "active"
			out.HasAccess = true
		} else {
			out.Status = "paymentRequired"
		} // Reserved for a verified recurring-payment integration.
		if sub.AutoRenew {
			out.NextRenewalAt = &end
			if out.Status == "trialing" {
				out.NextRenewalAt = sub.TrialEndsAt
			}
		} else if sub.AccessEndsAt != nil {
			out.PeriodEnd = sub.AccessEndsAt
		}
	}
	if !account.IsActive {
		out.Status = "businessInactive"
		out.HasAccess = false
	}
	return out
}
func subscriptionError(access SubscriptionAccess) error {
	if access.HasAccess {
		return nil
	}
	if access.Status == "businessInactive" {
		return apperr.New(403, "BUSINESS_INACTIVE", "This business is deactivated. Contact Harbor support")
	}
	if access.Status == "required" {
		return apperr.New(402, "SUBSCRIPTION_REQUIRED", "Choose a subscription plan to use this business")
	}
	return apperr.New(402, "SUBSCRIPTION_EXPIRED", "Your subscription has ended. Choose a plan to continue")
}
func (s *Store) RequireSubscription(ctx context.Context, a Actor) error {
	if err := allow(a, Admin, Manager, WarehouseStaff, DeliveryPartner); err != nil {
		return err
	}
	var account BusinessAccount
	if err := s.find(database.WithAccount(ctx, a.AccountID), "accounts", "accountId", a.AccountID, &account); err != nil {
		return err
	}
	return subscriptionError(subscriptionAccess(account, s.now()))
}
func (s *Store) planUsage(ctx context.Context) (PlanUsage, error) {
	var out PlanUsage
	active := bson.M{"isActive": true}
	var err error
	out.SKUs, err = s.col("variants").CountDocuments(ctx, active)
	if err != nil {
		return out, err
	}
	products, err := s.col("products").Distinct(ctx, "productId", active)
	if err != nil {
		return out, err
	}
	variantProducts, err := s.col("variants").Distinct(ctx, "productId", active)
	if err != nil {
		return out, err
	}
	covered := map[any]bool{}
	for _, id := range variantProducts {
		covered[id] = true
	}
	for _, id := range products {
		if !covered[id] {
			out.SKUs++
		}
	}
	out.Warehouses, err = s.col("warehouses").CountDocuments(ctx, active)
	if err != nil {
		return out, err
	}
	out.Partners, err = s.col("users").CountDocuments(ctx, bson.M{"isActive": true, "role": DeliveryPartner})
	return out, err
}
func withinPlan(plan Plan, usage PlanUsage) bool {
	return usage.SKUs <= plan.SKULimit && usage.Warehouses <= plan.WarehouseLimit && usage.Partners <= plan.PartnerLimit
}

// Caller holds the account revision lock, serializing capacity checks across
// different admins/managers and simultaneous plan changes/deactivations.
func (s *Store) checkCapacity(sc mongo.SessionContext, kind, parentID string) error {
	var account BusinessAccount
	id, err := database.AccountID(sc)
	if err != nil {
		return err
	}
	if err = s.find(sc, "accounts", "accountId", id, &account); err != nil {
		return err
	}
	access := subscriptionAccess(account, s.now())
	if err = subscriptionError(access); err != nil {
		return err
	}
	usage, err := s.planUsage(sc)
	if err != nil {
		return err
	}
	limit, current := int64(0), int64(0)
	switch kind {
	case "product":
		limit, current = access.Plan.SKULimit, usage.SKUs
	case "variant":
		limit, current = access.Plan.SKULimit, usage.SKUs
		// The first active variant takes its product's reserved slot.
		n, err := s.col("variants").CountDocuments(sc, bson.M{"productId": parentID, "isActive": true})
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
	case "warehouse":
		limit, current = access.Plan.WarehouseLimit, usage.Warehouses
	case "partner":
		limit, current = access.Plan.PartnerLimit, usage.Partners
	default:
		return apperr.Validation("Unsupported plan capacity")
	}
	if current >= limit {
		return apperr.Conflict("PLAN_LIMIT_REACHED", "Your plan's "+kind+" limit is reached. Upgrade your subscription to add more")
	}
	return nil
}
func (s *Store) SubscriptionStatus(ctx context.Context, a Actor) (SubscriptionAccess, error) {
	var out SubscriptionAccess
	if err := allow(a, Admin, Manager, WarehouseStaff, DeliveryPartner); err != nil {
		return out, err
	}
	ctx = database.WithAccount(ctx, a.AccountID)
	err := s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		var account BusinessAccount
		if err := s.find(sc, "accounts", "accountId", a.AccountID, &account); err != nil {
			return err
		}
		out = subscriptionAccess(account, s.now())
		if a.Role == Admin || a.Role == Manager {
			usage, err := s.planUsage(sc)
			if err != nil {
				return err
			}
			out.Usage = &usage
		}
		return nil
	})
	return out, err
}
func (s *Store) subscriptionTx(ctx context.Context, a Actor, fn func(mongo.SessionContext, *BusinessAccount) error) error {
	if err := allow(a, Admin); err != nil {
		return err
	}
	ctx = database.WithAccount(ctx, a.AccountID)
	return s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		var account BusinessAccount
		if err := s.lock(sc, "accounts", "accountId", a.AccountID, &account); err != nil {
			return err
		}
		var u User
		if err := s.lock(sc, "users", "userId", a.UserID, &u); err != nil {
			return err
		}
		if u.Role != Admin {
			return apperr.Forbidden()
		}
		return fn(sc, &account)
	})
}
func (s *Store) saveSubscription(sc mongo.SessionContext, a Actor, account *BusinessAccount, kind string) error {
	if err := s.update(sc, "accounts", "accountId", account.AccountID, bson.M{"subscription": account.Subscription, "trialUsed": account.TrialUsed}); err != nil {
		return err
	}
	id, err := s.next(sc, "subscriptionEvent", "SEV-")
	if err != nil {
		return err
	}
	return insert(sc, s.col("subscriptionEvents"), SubscriptionEvent{EventID: id, Kind: kind, ActorID: a.UserID, Subscription: *account.Subscription, CreatedAt: s.now().UTC()})
}
func (s *Store) Subscribe(ctx context.Context, a Actor, planID string) (SubscriptionAccess, error) {
	var out SubscriptionAccess
	if planID == "" {
		return out, apperr.Validation("planId is required")
	}
	err := s.subscriptionTx(ctx, a, func(sc mongo.SessionContext, account *BusinessAccount) error {
		var plan Plan
		err := s.rawIdentity("platformPlans").FindOneAndUpdate(sc, bson.M{"planId": planID, "isActive": true}, bson.M{"$inc": bson.M{"revision": 1}}).Decode(&plan)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return apperr.NotFound()
		}
		if err != nil {
			return err
		}
		if plan.Price != 0 {
			return apperr.Conflict("PAYMENT_SETUP_REQUIRED", "This plan requires a verified recurring payment setup")
		}
		usage, err := s.planUsage(sc)
		if err != nil {
			return err
		}
		if !withinPlan(plan, usage) {
			return apperr.Conflict("PLAN_BELOW_USAGE", "Choose a plan that covers your current SKUs, warehouses and delivery partners")
		}
		current := subscriptionAccess(*account, s.now())
		if current.HasAccess && account.Subscription.Plan.PlanID == planID {
			return apperr.Conflict("ALREADY_SUBSCRIBED", "This plan is already selected. Resume renewal if it was cancelled")
		}
		now := s.now().UTC().Truncate(time.Millisecond)
		id, err := s.next(sc, "subscription", "SUB-")
		if err != nil {
			return err
		}
		sub := &Subscription{SubscriptionID: id, Plan: plan, StartedAt: now, PeriodAnchor: now, AutoRenew: true}
		if !account.TrialUsed && plan.TrialDays > 0 {
			end := now.AddDate(0, 0, plan.TrialDays)
			sub.TrialEndsAt = &end
			sub.PeriodAnchor = end
		} else if account.Subscription != nil && account.Subscription.TrialEndsAt != nil && now.Before(*account.Subscription.TrialEndsAt) {
			sub.TrialEndsAt = account.Subscription.TrialEndsAt
			sub.PeriodAnchor = *sub.TrialEndsAt
		}
		account.Subscription = sub
		account.TrialUsed = true
		if err = s.saveSubscription(sc, a, account, "subscribed"); err != nil {
			return err
		}
		out = subscriptionAccess(*account, now)
		out.Usage = &usage
		return nil
	})
	return out, err
}
func (s *Store) SetSubscriptionRenewal(ctx context.Context, a Actor, renew bool) (SubscriptionAccess, error) {
	var out SubscriptionAccess
	err := s.subscriptionTx(ctx, a, func(sc mongo.SessionContext, account *BusinessAccount) error {
		access := subscriptionAccess(*account, s.now())
		if err := subscriptionError(access); err != nil {
			return err
		}
		if account.Subscription.AutoRenew == renew {
			out = access
			return nil
		}
		account.Subscription.AutoRenew = renew
		kind := "renewalResumed"
		if renew {
			account.Subscription.AccessEndsAt = nil
		} else {
			kind = "renewalCancelled"
			end := access.PeriodEnd
			if access.Status == "trialing" {
				end = access.TrialEndsAt
			}
			account.Subscription.AccessEndsAt = end
		}
		if err := s.saveSubscription(sc, a, account, kind); err != nil {
			return err
		}
		out = subscriptionAccess(*account, s.now())
		return nil
	})
	return out, err
}
