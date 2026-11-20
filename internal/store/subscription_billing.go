package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"harbor/internal/apperr"
	"harbor/internal/database"
)

// Platform billing is separate from every tenant's sales invoices and payments.
// CurrentBill.PaidAmount is a server-maintained receipt sum, never editable input.
type SubscriptionBill struct {
	BillID         string     `json:"billId" bson:"billId"`
	AccountID      string     `json:"accountId" bson:"accountId"`
	SubscriptionID string     `json:"subscriptionId" bson:"subscriptionId"`
	Cycle          int64      `json:"cycle" bson:"cycle"`
	Plan           Plan       `json:"plan" bson:"plan"`
	Subtotal       float64    `json:"subtotal" bson:"subtotal"`
	Discount       *Discount  `json:"discount,omitempty" bson:"discount,omitempty"`
	DiscountAmount float64    `json:"discountAmount" bson:"discountAmount"`
	Total          float64    `json:"total" bson:"total"`
	PaidAmount     float64    `json:"paidAmount" bson:"paidAmount"`
	Status         string     `json:"status" bson:"status"`
	CreatedAt      time.Time  `json:"createdAt" bson:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt" bson:"updatedAt"`
	PaidAt         *time.Time `json:"paidAt,omitempty" bson:"paidAt,omitempty"`
	PeriodStart    *time.Time `json:"periodStart,omitempty" bson:"periodStart,omitempty"`
	PeriodEnd      *time.Time `json:"periodEnd,omitempty" bson:"periodEnd,omitempty"`
}
type SubscriptionQuote struct {
	BillID         string    `json:"billId,omitempty"`
	Subtotal       float64   `json:"subtotal"`
	Discount       *Discount `json:"discount,omitempty"`
	DiscountAmount float64   `json:"discountAmount"`
	Total          float64   `json:"total"`
	PaidAmount     float64   `json:"paidAmount"`
	AmountDue      float64   `json:"amountDue"`
}

func subscriptionQuote(sub Subscription) SubscriptionQuote {
	out := SubscriptionQuote{Subtotal: sub.Plan.Price, Total: sub.Plan.Price, AmountDue: sub.Plan.Price}
	if b := sub.CurrentBill; b != nil && b.Status == "pending" && b.SubscriptionID == sub.SubscriptionID {
		out = SubscriptionQuote{BillID: b.BillID, Subtotal: b.Subtotal, Discount: b.Discount, DiscountAmount: b.DiscountAmount, Total: b.Total, PaidAmount: b.PaidAmount, AmountDue: money(cents(b.Total) - cents(b.PaidAmount))}
	}
	return out
}

type SubscriptionPayment struct {
	PaymentID      string    `json:"paymentId" bson:"paymentId"`
	AccountID      string    `json:"accountId" bson:"accountId"`
	SubscriptionID string    `json:"subscriptionId" bson:"subscriptionId"`
	BillID         string    `json:"billId" bson:"billId"`
	RequestID      string    `json:"requestId" bson:"requestId"`
	Amount         float64   `json:"amount" bson:"amount"`
	Method         string    `json:"method" bson:"method"`
	Reference      string    `json:"reference" bson:"reference"`
	Note           string    `json:"note" bson:"note"`
	RecordedBy     string    `json:"recordedBy" bson:"recordedBy"`
	CreatedAt      time.Time `json:"createdAt" bson:"createdAt"`
}
type SubscriptionPaymentInput struct {
	BillID    string  `json:"billId"`
	RequestID string  `json:"requestId"`
	Amount    float64 `json:"amount"`
	Method    string  `json:"method"`
	Reference string  `json:"reference"`
	Note      string  `json:"note"`
}
type SubscriptionPaymentResult struct {
	Payment SubscriptionPayment `json:"payment"`
	Bill    SubscriptionBill    `json:"bill"`
	Access  SubscriptionAccess  `json:"access"`
}

func (s *Store) platformBillingTx(ctx context.Context, a Actor, accountID string, fn func(mongo.SessionContext, *BusinessAccount) error) error {
	if !database.ValidAccountID(accountID) {
		return apperr.Validation("A valid business account is required")
	}
	return s.platformTx(ctx, a, func(sc mongo.SessionContext) error {
		var account BusinessAccount
		err := s.rawIdentity("accounts").FindOneAndUpdate(sc, bson.M{"accountId": accountID}, bson.M{"$inc": bson.M{"revision": 1}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&account)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return apperr.NotFound()
		}
		if err != nil {
			return err
		}
		return fn(scopedSession(sc, accountID), &account)
	})
}
func (s *Store) PlatformSubscription(ctx context.Context, a Actor, accountID string) (SubscriptionAccess, error) {
	var out SubscriptionAccess
	if _, err := s.platformUser(ctx, a); err != nil {
		return out, err
	}
	if !database.ValidAccountID(accountID) {
		return out, apperr.Validation("A valid business account is required")
	}
	var account BusinessAccount
	err := s.rawIdentity("accounts").FindOne(ctx, bson.M{"accountId": accountID}).Decode(&account)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return out, apperr.NotFound()
	}
	if err != nil {
		return out, err
	}
	return subscriptionAccess(account, s.now()), nil
}
func (s *Store) ensureSubscriptionBill(sc mongo.SessionContext, account *BusinessAccount) (*SubscriptionBill, error) {
	sub := account.Subscription
	if sub == nil {
		return nil, apperr.Conflict("SUBSCRIPTION_REQUIRED", "The business owner must select a plan first")
	}
	if sub.Plan.Price <= 0 {
		return nil, apperr.Conflict("FREE_PLAN", "This is a free plan and does not require payment")
	}
	if sub.CurrentBill != nil && sub.CurrentBill.Status == "pending" {
		return sub.CurrentBill, nil
	}
	id, err := s.globalNext(sc, "subscriptionBill", "SBI-")
	if err != nil {
		return nil, err
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	bill := &SubscriptionBill{BillID: id, AccountID: account.AccountID, SubscriptionID: sub.SubscriptionID, Cycle: sub.BillingCycle + 1, Plan: sub.Plan, Subtotal: sub.Plan.Price, Total: sub.Plan.Price, Status: "pending", CreatedAt: now, UpdatedAt: now}
	if _, err = s.rawIdentity("subscriptionBills").InsertOne(sc, bill); err != nil {
		return nil, err
	}
	sub.CurrentBill = bill
	return bill, nil
}
func (s *Store) PrepareSubscriptionBill(ctx context.Context, a Actor, accountID string) (SubscriptionBill, error) {
	var out SubscriptionBill
	err := s.platformBillingTx(ctx, a, accountID, func(sc mongo.SessionContext, account *BusinessAccount) error {
		bill, err := s.ensureSubscriptionBill(sc, account)
		if err != nil {
			return err
		}
		if err = s.saveSubscription(sc, a, account, "billPrepared"); err != nil {
			return err
		}
		out = *bill
		return nil
	})
	return out, err
}
func (s *Store) SetSubscriptionCoupon(ctx context.Context, a Actor, code string) (SubscriptionAccess, error) {
	var out SubscriptionAccess
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) > 32 || (code != "" && !couponCode.MatchString(code)) {
		return out, apperr.Validation("Enter a valid subscription coupon code")
	}
	err := s.subscriptionTx(ctx, a, func(sc mongo.SessionContext, account *BusinessAccount) error {
		bill, err := s.ensureSubscriptionBill(sc, account)
		if err != nil {
			return err
		}
		if cents(bill.PaidAmount) > 0 {
			return apperr.Conflict("BILL_LOCKED", "Coupons cannot be changed after a payment has been recorded")
		}
		var discount *Discount
		if code != "" {
			var coupon Coupon
			err = s.rawIdentity("subscriptionCoupons").FindOneAndUpdate(sc, bson.M{"code": code, "isActive": true}, bson.M{"$inc": bson.M{"revision": 1}}).Decode(&coupon)
			if errors.Is(err, mongo.ErrNoDocuments) {
				return apperr.Validation("This subscription coupon is invalid or archived")
			}
			if err != nil {
				return err
			}
			discount = &Discount{CouponID: coupon.CouponID, Code: coupon.Code, Name: coupon.Name, Type: coupon.Type, Value: coupon.Value}
		}
		bill.Discount = discount
		bill.DiscountAmount = money(discountCents(discount, cents(bill.Subtotal), cents(bill.Subtotal)))
		bill.Total = money(cents(bill.Subtotal) - cents(bill.DiscountAmount))
		bill.UpdatedAt = s.now().UTC().Truncate(time.Millisecond)
		fields := bson.M{"discount": bill.Discount, "discountAmount": bill.DiscountAmount, "total": bill.Total, "updatedAt": bill.UpdatedAt}
		if _, err = s.rawIdentity("subscriptionBills").UpdateOne(sc, bson.M{"billId": bill.BillID, "accountId": account.AccountID, "status": "pending"}, bson.M{"$set": fields}); err != nil {
			return err
		}
		if err = s.saveSubscription(sc, a, account, "subscriptionCouponChanged"); err != nil {
			return err
		}
		out = subscriptionAccess(*account, s.now())
		return nil
	})
	return out, err
}
func validateSubscriptionPayment(in SubscriptionPaymentInput) error {
	if in.BillID == "" || len(in.RequestID) < 16 || len(in.RequestID) > 128 || strings.TrimSpace(in.RequestID) != in.RequestID {
		return apperr.Validation("billId and a 16–128 byte requestId are required")
	}
	if err := validMoney(in.Amount, true); err != nil {
		return err
	}
	if !validMethod(in.Method) && in.Method != "waiver" {
		return apperr.Validation("method must be cash, upi, bank, or waiver")
	}
	if strings.TrimSpace(in.Note) == "" {
		return apperr.Validation("A note is required for a manually recorded subscription payment")
	}
	if err := validText(in.Note); err != nil {
		return err
	}
	if len(in.Reference) > 200 {
		return apperr.Validation("reference must be at most 200 bytes")
	}
	if (in.Method == "upi" || in.Method == "bank") && strings.TrimSpace(in.Reference) == "" {
		return apperr.Validation("A transaction reference is required for UPI or bank payments")
	}
	return nil
}
func sameSubscriptionPayment(old SubscriptionPayment, accountID string, in SubscriptionPaymentInput) bool {
	return old.AccountID == accountID && old.BillID == in.BillID && cents(old.Amount) == cents(in.Amount) && old.Method == in.Method && old.Reference == in.Reference && old.Note == in.Note
}

// Late renewal starts a fresh period at approval, so blocked days are not billed.
// An early payment extends the current paid/trial boundary, retaining its Indian
// calendar anchor across short months and leap years.
func subscriptionCoverage(sub Subscription, now time.Time) (time.Time, time.Time, time.Time) {
	start := now
	anchor := now
	if sub.TrialEndsAt != nil && !sub.TrialEndsAt.Before(start) {
		start = *sub.TrialEndsAt
		anchor = sub.PeriodAnchor
	}
	if sub.PaidUntil != nil && !sub.PaidUntil.Before(start) {
		start = *sub.PaidUntil
		anchor = sub.PeriodAnchor
	}
	_, end := billingPeriod(anchor, start, sub.Plan.BillingInterval)
	return start, end, anchor
}
func (s *Store) RecordSubscriptionPayment(ctx context.Context, a Actor, accountID string, in SubscriptionPaymentInput) (SubscriptionPaymentResult, error) {
	var out SubscriptionPaymentResult
	in.Note = strings.TrimSpace(in.Note)
	in.Reference = strings.TrimSpace(in.Reference)
	if err := validateSubscriptionPayment(in); err != nil {
		return out, err
	}
	err := s.platformBillingTx(ctx, a, accountID, func(sc mongo.SessionContext, account *BusinessAccount) error {
		out = SubscriptionPaymentResult{}
		var old SubscriptionPayment
		err := s.rawIdentity("subscriptionPayments").FindOne(sc, bson.M{"requestId": in.RequestID}).Decode(&old)
		if err == nil {
			if !sameSubscriptionPayment(old, accountID, in) {
				return apperr.Conflict("IDEMPOTENCY_CONFLICT", "This requestId was already used for different subscription payment details")
			}
			if err = s.rawIdentity("subscriptionBills").FindOne(sc, bson.M{"billId": old.BillID, "accountId": accountID}).Decode(&out.Bill); err != nil {
				return err
			}
			out.Payment = old
			out.Access = subscriptionAccess(*account, s.now())
			return nil
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		sub := account.Subscription
		if sub == nil || sub.CurrentBill == nil || sub.CurrentBill.BillID != in.BillID || sub.CurrentBill.SubscriptionID != sub.SubscriptionID {
			return apperr.Conflict("BILL_CHANGED", "The subscription bill has changed. Refresh before recording payment")
		}
		bill := sub.CurrentBill
		if bill.Status != "pending" {
			return apperr.Conflict("BILL_ALREADY_PAID", "This subscription bill has already been paid")
		}
		// Sum append-only receipts rather than accepting an editable paid balance.
		cur, err := s.rawIdentity("subscriptionPayments").Find(sc, bson.M{"billId": bill.BillID, "accountId": accountID})
		if err != nil {
			return err
		}
		var prior []SubscriptionPayment
		err = cur.All(sc, &prior)
		cur.Close(sc)
		if err != nil {
			return err
		}
		var paid int64
		for _, p := range prior {
			paid += cents(p.Amount)
		}
		remaining := cents(bill.Total) - paid
		if cents(in.Amount) > remaining || (remaining > 0 && cents(in.Amount) <= 0) {
			return apperr.Validation("Amount must be greater than zero and cannot exceed the outstanding subscription bill")
		}
		if (remaining == 0 && in.Method != "waiver") || (remaining > 0 && in.Method == "waiver") {
			return apperr.Validation("Use waiver only for a fully discounted zero-amount bill")
		}
		id, err := s.globalNext(sc, "subscriptionPayment", "SPR-")
		if err != nil {
			return err
		}
		now := s.now().UTC().Truncate(time.Millisecond)
		out.Payment = SubscriptionPayment{PaymentID: id, AccountID: accountID, SubscriptionID: sub.SubscriptionID, BillID: bill.BillID, RequestID: in.RequestID, Amount: RoundMoney(in.Amount), Method: in.Method, Reference: in.Reference, Note: in.Note, RecordedBy: a.UserID, CreatedAt: now}
		if _, err = s.rawIdentity("subscriptionPayments").InsertOne(sc, out.Payment); err != nil {
			return err
		}
		bill.PaidAmount = money(paid + cents(in.Amount))
		bill.UpdatedAt = now
		if cents(bill.PaidAmount) == cents(bill.Total) {
			start, end, anchor := subscriptionCoverage(*sub, now)
			bill.Status = "paid"
			bill.PaidAt = &now
			bill.PeriodStart = &start
			bill.PeriodEnd = &end
			sub.PaidFrom = &start
			sub.PaidUntil = &end
			sub.PeriodAnchor = anchor
			sub.BillingCycle = bill.Cycle
			if !sub.AutoRenew {
				sub.AccessEndsAt = &end
			}
		}
		fields := bson.M{"paidAmount": bill.PaidAmount, "status": bill.Status, "updatedAt": bill.UpdatedAt, "paidAt": bill.PaidAt, "periodStart": bill.PeriodStart, "periodEnd": bill.PeriodEnd}
		if _, err = s.rawIdentity("subscriptionBills").UpdateOne(sc, bson.M{"billId": bill.BillID, "accountId": accountID}, bson.M{"$set": fields}); err != nil {
			return err
		}
		if err = s.saveSubscription(sc, a, account, "subscriptionPaymentRecorded"); err != nil {
			return err
		}
		if err = s.platformEvent(sc, a, "subscriptionPaymentRecorded", accountID, id); err != nil {
			return err
		}
		out.Bill = *bill
		out.Access = subscriptionAccess(*account, now)
		return nil
	})
	return out, err
}
func (s *Store) SubscriptionPayments(ctx context.Context, a Actor, accountID string, p Page, platform bool) (List[SubscriptionPayment], error) {
	out := List[SubscriptionPayment]{Items: []SubscriptionPayment{}, Page: p.Page, Limit: p.Limit}
	if err := ValidatePage(p); err != nil {
		return out, err
	}
	if platform {
		if _, err := s.platformUser(ctx, a); err != nil {
			return out, err
		}
		if !database.ValidAccountID(accountID) {
			return out, apperr.Validation("A valid business account is required")
		}
	} else {
		if err := allow(a, Admin); err != nil {
			return out, err
		}
		accountID = a.AccountID
	}
	f := bson.M{"accountId": accountID}
	var err error
	out.Total, err = s.rawIdentity("subscriptionPayments").CountDocuments(ctx, f)
	if err != nil {
		return out, err
	}
	cur, err := s.rawIdentity("subscriptionPayments").Find(ctx, f, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "paymentId", Value: -1}}).SetSkip(int64(p.Page-1)*int64(p.Limit)).SetLimit(int64(p.Limit)))
	if err != nil {
		return out, err
	}
	defer cur.Close(ctx)
	err = cur.All(ctx, &out.Items)
	return out, err
}
func (s *Store) SaveSubscriptionCoupon(ctx context.Context, a Actor, id string, in CouponInput) (Coupon, error) {
	var out Coupon
	in, err := normalizeCoupon(in)
	if err != nil {
		return out, err
	}
	err = s.platformTx(ctx, a, func(sc mongo.SessionContext) error {
		if id != "" {
			err := s.rawIdentity("subscriptionCoupons").FindOne(sc, bson.M{"couponId": id, "isActive": true}).Decode(&out)
			if errors.Is(err, mongo.ErrNoDocuments) {
				return apperr.NotFound()
			}
			if err != nil {
				return err
			}
		}
		n, err := s.rawIdentity("subscriptionCoupons").CountDocuments(sc, bson.M{"code": in.Code, "couponId": bson.M{"$ne": id}})
		if err != nil {
			return err
		}
		if n > 0 {
			return apperr.Conflict("COUPON_CODE_EXISTS", "This subscription coupon code already exists")
		}
		kind := "subscriptionCouponUpdated"
		if id == "" {
			cid, err := s.globalNext(sc, "subscriptionCoupon", "SCP-")
			if err != nil {
				return err
			}
			out = Coupon{Base: s.base(), CouponID: cid, Name: in.Name, Code: in.Code, Type: in.Type, Value: in.Value}
			if _, err = s.rawIdentity("subscriptionCoupons").InsertOne(sc, out); err != nil {
				return err
			}
			kind = "subscriptionCouponCreated"
		} else {
			out.Name = in.Name
			out.Code = in.Code
			out.Type = in.Type
			out.Value = in.Value
			out.UpdatedAt = s.now().UTC()
			if _, err = s.rawIdentity("subscriptionCoupons").UpdateOne(sc, bson.M{"couponId": id}, bson.M{"$set": bson.M{"name": in.Name, "code": in.Code, "type": in.Type, "value": in.Value, "updatedAt": out.UpdatedAt}, "$inc": bson.M{"revision": 1}}); err != nil {
				return err
			}
		}
		return s.platformEvent(sc, a, kind, out.CouponID, in.Code)
	})
	return out, err
}
func (s *Store) SubscriptionCoupons(ctx context.Context, a Actor, p Page, active string) (List[Coupon], error) {
	out := List[Coupon]{Items: []Coupon{}, Page: p.Page, Limit: p.Limit}
	if _, err := s.platformUser(ctx, a); err != nil {
		return out, err
	}
	if err := ValidatePage(p); err != nil {
		return out, err
	}
	f := bson.M{}
	if active != "" {
		if active != "true" && active != "false" {
			return out, apperr.Validation("isActive must be true or false")
		}
		f["isActive"] = active == "true"
	}
	var err error
	out.Total, err = s.rawIdentity("subscriptionCoupons").CountDocuments(ctx, f)
	if err != nil {
		return out, err
	}
	cur, err := s.rawIdentity("subscriptionCoupons").Find(ctx, f, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "couponId", Value: -1}}).SetSkip(int64(p.Page-1)*int64(p.Limit)).SetLimit(int64(p.Limit)))
	if err != nil {
		return out, err
	}
	defer cur.Close(ctx)
	err = cur.All(ctx, &out.Items)
	return out, err
}
func (s *Store) ArchiveSubscriptionCoupon(ctx context.Context, a Actor, id string) error {
	return s.platformTx(ctx, a, func(sc mongo.SessionContext) error {
		res, err := s.rawIdentity("subscriptionCoupons").UpdateOne(sc, bson.M{"couponId": id, "isActive": true}, bson.M{"$set": bson.M{"isActive": false, "updatedAt": s.now().UTC()}, "$inc": bson.M{"revision": 1}})
		if err != nil {
			return err
		}
		if res.MatchedCount == 0 {
			return apperr.NotFound()
		}
		return s.platformEvent(sc, a, "subscriptionCouponArchived", id, "")
	})
}
