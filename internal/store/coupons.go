package store

import (
	"context"
	"math/big"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"harbor/internal/apperr"
	"harbor/internal/database"
)

type CouponInput struct {
	Name  string  `json:"name"`
	Code  string  `json:"code"`
	Type  string  `json:"type"`
	Value float64 `json:"value"`
}
type Coupon struct {
	Base     `bson:",inline"`
	CouponID string  `json:"couponId" bson:"couponId"`
	Name     string  `json:"name" bson:"name"`
	Code     string  `json:"code" bson:"code"`
	Type     string  `json:"type" bson:"type"`
	Value    float64 `json:"value" bson:"value"`
}
type DiscountInput struct {
	Type   string  `json:"type"`
	Value  float64 `json:"value"`
	Reason string  `json:"reason"`
}

// Discount terms are immutable snapshots. Coupon changes affect new orders only.
type Discount struct {
	CouponID string  `json:"couponId,omitempty" bson:"couponId,omitempty"`
	Code     string  `json:"code,omitempty" bson:"code,omitempty"`
	Name     string  `json:"name,omitempty" bson:"name,omitempty"`
	Type     string  `json:"type" bson:"type"`
	Value    float64 `json:"value" bson:"value"`
	Reason   string  `json:"reason,omitempty" bson:"reason,omitempty"`
}

var couponCode = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{1,31}$`)

func validateDiscount(kind string, value float64) error {
	if kind != "percentage" && kind != "fixed" {
		return apperr.Validation("Discount type must be percentage or fixed")
	}
	if err := validMoney(value, false); err != nil {
		return err
	}
	if kind == "percentage" && value > 100 {
		return apperr.Validation("Discount percentage must be greater than zero and at most 100")
	}
	return nil
}
func normalizeCoupon(in CouponInput) (CouponInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	if err := validName(in.Name); err != nil {
		return in, err
	}
	if !couponCode.MatchString(in.Code) {
		return in, apperr.Validation("Coupon code must be 2–32 letters, numbers, underscores or hyphens")
	}
	if err := validateDiscount(in.Type, in.Value); err != nil {
		return in, err
	}
	in.Value = RoundMoney(in.Value)
	return in, nil
}
func (s *Store) SaveCoupon(ctx context.Context, a Actor, id string, in CouponInput) (Coupon, error) {
	var out Coupon
	in, err := normalizeCoupon(in)
	if err != nil {
		return out, err
	}
	err = s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, _ Actor) error {
		if id != "" {
			if err := s.lock(sc, "coupons", "couponId", id, &out); err != nil {
				return err
			}
		}
		n, err := s.col("coupons").CountDocuments(sc, bson.M{"code": in.Code, "couponId": bson.M{"$ne": id}})
		if err != nil {
			return err
		}
		if n > 0 {
			return apperr.Conflict("COUPON_CODE_EXISTS", "This coupon code already exists in your business")
		}
		if id == "" {
			newID, err := s.next(sc, "coupon", "CPN-")
			if err != nil {
				return err
			}
			out.Base = s.base()
			out.CouponID = newID
			out.Name, out.Code, out.Type, out.Value = in.Name, in.Code, in.Type, in.Value
			return insert(sc, s.col("coupons"), out)
		}
		out.Name, out.Code, out.Type, out.Value = in.Name, in.Code, in.Type, in.Value
		out.UpdatedAt = s.now().UTC()
		return s.update(sc, "coupons", "couponId", id, bson.M{"name": in.Name, "code": in.Code, "type": in.Type, "value": in.Value})
	})
	return out, err
}
func (s *Store) Coupons(ctx context.Context, a Actor, p Page, active string, optionsOnly bool) (List[Coupon], error) {
	var out List[Coupon]
	roles := []string{Admin, Manager}
	if optionsOnly {
		roles = append(roles, WarehouseStaff, DeliveryPartner)
	}
	if err := allow(a, roles...); err != nil {
		return out, err
	}
	if err := ValidatePage(p); err != nil {
		return out, err
	}
	f := bson.M{}
	if optionsOnly {
		f["isActive"] = true
	} else if active != "" {
		if active != "true" && active != "false" {
			return out, apperr.Validation("isActive must be true or false")
		}
		f["isActive"] = active == "true"
	}
	return list[Coupon](database.WithAccount(ctx, a.AccountID), s, "coupons", f, p, bson.D{{Key: "createdAt", Value: -1}, {Key: "couponId", Value: -1}})
}
func (s *Store) orderDiscount(sc mongo.SessionContext, a Actor, in OrderInput, subtotal int64) (*Discount, error) {
	if in.CouponID != "" && in.Discount != nil {
		return nil, apperr.Validation("Choose a coupon or a manual discount, not both")
	}
	var d *Discount
	if in.CouponID != "" {
		var c Coupon
		if err := s.lock(sc, "coupons", "couponId", in.CouponID, &c); err != nil {
			return nil, err
		}
		d = &Discount{CouponID: c.CouponID, Code: c.Code, Name: c.Name, Type: c.Type, Value: c.Value}
	} else if in.Discount != nil {
		if err := allow(a, Admin, Manager); err != nil {
			return nil, err
		}
		if err := validateDiscount(in.Discount.Type, in.Discount.Value); err != nil {
			return nil, err
		}
		reason := strings.TrimSpace(in.Discount.Reason)
		if reason == "" {
			return nil, apperr.Validation("A reason is required for a manual discount")
		}
		if err := validText(reason); err != nil {
			return nil, err
		}
		d = &Discount{Type: in.Discount.Type, Value: RoundMoney(in.Discount.Value), Reason: reason}
	}
	if d != nil && d.Type == "fixed" && cents(d.Value) > subtotal {
		return nil, apperr.Validation("Fixed discount cannot exceed the order subtotal at current prices")
	}
	return d, nil
}

// roundedRatio uses integer arithmetic without overflowing when money values are
// multiplied. All inputs are nonnegative cents, with half cents rounded up.
func roundedRatio(value, numerator, denominator int64) int64 {
	if denominator <= 0 {
		return 0
	}
	var n, b, d big.Int
	n.Mul(big.NewInt(value), big.NewInt(numerator))
	d.SetInt64(denominator)
	b.Quo(&d, big.NewInt(2))
	n.Add(&n, &b)
	return n.Quo(&n, &d).Int64()
}
func discountCents(d *Discount, deliveredSubtotal, orderSubtotal int64) int64 {
	if d == nil || deliveredSubtotal == 0 {
		return 0
	}
	if d.Type == "percentage" {
		return roundedRatio(deliveredSubtotal, cents(d.Value), 10000)
	}
	value := cents(d.Value)
	if value > orderSubtotal {
		value = orderSubtotal
	}
	return roundedRatio(value, deliveredSubtotal, orderSubtotal)
}

// Allocate proportional cents with a deterministic largest-remainder rule, so
// line net amounts sum exactly to the invoice net total (including free orders).
func applyInvoiceDiscount(lines []InvoiceLine, discount int64) {
	var subtotal, used int64
	for _, l := range lines {
		subtotal += cents(l.Amount)
	}
	alloc := make([]int64, len(lines))
	remainders := make([]big.Int, len(lines))
	for i, l := range lines {
		var product, quotient big.Int
		product.Mul(big.NewInt(discount), big.NewInt(cents(l.Amount)))
		if subtotal > 0 {
			quotient.QuoRem(&product, big.NewInt(subtotal), &remainders[i])
			alloc[i] = quotient.Int64()
		}
		used += alloc[i]
	}
	for left := discount - used; left > 0; left-- {
		best := 0
		for i := 1; i < len(lines); i++ {
			if remainders[i].Cmp(&remainders[best]) > 0 {
				best = i
			}
		}
		alloc[best]++
		remainders[best].SetInt64(-1)
	}
	for i := range lines {
		lines[i].Subtotal = lines[i].Amount
		lines[i].DiscountAmount = money(alloc[i])
		lines[i].Amount = money(cents(lines[i].Amount) - alloc[i])
	}
}
