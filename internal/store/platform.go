package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"
	"harbor/internal/apperr"
	"harbor/internal/database"
)

const SuperAdmin = "superAdmin"
const platformAccount = "platform"

type PlanInput struct {
	Name            string `json:"name" bson:"name"`
	Description     string `json:"description" bson:"description"`
	SKULimit        int64  `json:"skuLimit" bson:"skuLimit"`
	WarehouseLimit  int64  `json:"warehouseLimit" bson:"warehouseLimit"`
	PartnerLimit    int64  `json:"partnerLimit" bson:"partnerLimit"`
	TrialDays       int    `json:"trialDays" bson:"trialDays"`
	BillingInterval string `json:"billingInterval" bson:"billingInterval"`
}
type Plan struct {
	Base      `bson:",inline"`
	PlanID    string `json:"planId" bson:"planId"`
	PlanInput `bson:",inline"`
	Price     float64 `json:"price" bson:"price"`
	Currency  string  `json:"currency" bson:"currency"`
}
type OwnerDetails struct {
	UserID   string `json:"userId" bson:"userId"`
	Name     string `json:"name" bson:"name"`
	Username string `json:"username" bson:"username"`
	Email    string `json:"email" bson:"email"`
	Phone    string `json:"phone" bson:"phone"`
}

// This allowlisted DTO never embeds operational account settings, staff,
// inventory, customers, invoices, payments or sales aggregates.
type BusinessRegistration struct {
	AccountID string `json:"accountId"`
	BusinessProfileInput
	Owner        OwnerDetails       `json:"owner"`
	IsActive     bool               `json:"isActive"`
	CreatedAt    time.Time          `json:"createdAt"`
	UpdatedAt    time.Time          `json:"updatedAt"`
	StatusReason string             `json:"statusReason"`
	Subscription SubscriptionAccess `json:"subscription"`
}
type BusinessStatusInput struct {
	IsActive *bool  `json:"isActive"`
	Reason   string `json:"reason"`
}
type PlatformEvent struct {
	EventID     string    `json:"eventId" bson:"eventId"`
	ActorID     string    `json:"actorId" bson:"actorId"`
	Kind        string    `json:"kind" bson:"kind"`
	ReferenceID string    `json:"referenceId" bson:"referenceId"`
	Reason      string    `json:"reason" bson:"reason"`
	CreatedAt   time.Time `json:"createdAt" bson:"createdAt"`
}

func validatePlan(in PlanInput) error {
	if err := validName(in.Name); err != nil {
		return err
	}
	if err := validText(in.Description); err != nil {
		return err
	}
	if in.SKULimit < 1 || in.SKULimit > 1000000 || in.WarehouseLimit < 1 || in.WarehouseLimit > 100000 || in.PartnerLimit < 0 || in.PartnerLimit > 100000 {
		return apperr.Validation("SKU limit must be 1–1,000,000, warehouse limit 1–100,000 and partner limit 0–100,000")
	}
	if in.TrialDays < 0 || in.TrialDays > 365 {
		return apperr.Validation("trialDays must be between 0 and 365")
	}
	if in.BillingInterval != "monthly" && in.BillingInterval != "yearly" {
		return apperr.Validation("billingInterval must be monthly or yearly")
	}
	return nil
}
func (s *Store) globalNext(sc mongo.SessionContext, name, prefix string) (string, error) {
	var counter struct {
		Seq int64 `bson:"seq"`
	}
	err := s.rawIdentity("platformCounters").FindOneAndUpdate(sc, bson.M{"counterName": name}, bson.M{"$inc": bson.M{"seq": 1}}, options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&counter)
	return fmt.Sprintf("%s%06d", prefix, counter.Seq), err
}
func (s *Store) BootstrapSuperAdmin(ctx context.Context, username, password string) error {
	if username == "" && password == "" {
		return nil
	}
	username = strings.ToLower(strings.TrimSpace(username))
	if err := validateCredentials(username, password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		var u User
		err := s.rawIdentity("users").FindOne(sc, bson.M{"username": username}).Decode(&u)
		if err == nil {
			if u.Role != SuperAdmin || u.AccountID != platformAccount || !u.IsActive {
				return apperr.Conflict("BOOTSTRAP_CONFLICT", "Superadmin login must be separate from every tenant login")
			}
			return nil // Deployment never silently resets an existing password.
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		id, err := s.globalNext(sc, "superAdminUser", "USR-")
		if err != nil {
			return err
		}
		_, err = s.rawIdentity("users").InsertOne(sc, User{Base: s.base(), UserID: id, AccountID: platformAccount, Name: "Harbor owner", Username: username, PasswordHash: string(hash), Role: SuperAdmin, LocationIDs: []string{}})
		return err
	})
}
func (s *Store) platformUser(ctx context.Context, a Actor) (User, error) {
	var u User
	if a.Role != SuperAdmin || a.AccountID != platformAccount {
		return u, apperr.Forbidden()
	}
	err := s.rawIdentity("users").FindOne(ctx, bson.M{"userId": a.UserID, "accountId": platformAccount, "role": SuperAdmin, "isActive": true}).Decode(&u)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return u, invalidAuth
	}
	return u, err
}
func (s *Store) platformTx(ctx context.Context, a Actor, fn func(mongo.SessionContext) error) error {
	if a.Role != SuperAdmin || a.AccountID != platformAccount {
		return apperr.Forbidden()
	}
	return s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		var u User
		err := s.rawIdentity("users").FindOneAndUpdate(sc, bson.M{"userId": a.UserID, "accountId": platformAccount, "role": SuperAdmin, "isActive": true}, bson.M{"$inc": bson.M{"revision": 1}}).Decode(&u)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return invalidAuth
		}
		if err != nil {
			return err
		}
		return fn(sc)
	})
}
func (s *Store) platformEvent(sc mongo.SessionContext, a Actor, kind, id, reason string) error {
	eid, err := s.globalNext(sc, "platformEvent", "PEV-")
	if err != nil {
		return err
	}
	_, err = s.rawIdentity("platformEvents").InsertOne(sc, PlatformEvent{EventID: eid, ActorID: a.UserID, Kind: kind, ReferenceID: id, Reason: reason, CreatedAt: s.now().UTC()})
	return err
}
func (s *Store) SavePlan(ctx context.Context, a Actor, id string, in PlanInput) (Plan, error) {
	var out Plan
	if err := validatePlan(in); err != nil {
		return out, err
	}
	in.Name = strings.TrimSpace(in.Name)
	err := s.platformTx(ctx, a, func(sc mongo.SessionContext) error {
		if id == "" {
			pid, err := s.globalNext(sc, "plan", "PLN-")
			if err != nil {
				return err
			}
			out = Plan{Base: s.base(), PlanID: pid, PlanInput: in, Currency: "INR"}
			if _, err = s.rawIdentity("platformPlans").InsertOne(sc, out); err != nil {
				return err
			}
			return s.platformEvent(sc, a, "planCreated", pid, in.Name)
		}
		err := s.rawIdentity("platformPlans").FindOne(sc, bson.M{"planId": id, "isActive": true}).Decode(&out)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return apperr.NotFound()
		}
		if err != nil {
			return err
		}
		out.PlanInput = in
		out.UpdatedAt = s.now().UTC()
		fields := bson.M{"name": in.Name, "description": in.Description, "skuLimit": in.SKULimit, "warehouseLimit": in.WarehouseLimit, "partnerLimit": in.PartnerLimit, "trialDays": in.TrialDays, "billingInterval": in.BillingInterval, "updatedAt": out.UpdatedAt}
		if _, err = s.rawIdentity("platformPlans").UpdateOne(sc, bson.M{"planId": id}, bson.M{"$set": fields, "$inc": bson.M{"revision": 1}}); err != nil {
			return err
		}
		return s.platformEvent(sc, a, "planUpdated", id, in.Name)
	})
	return out, err
}
func (s *Store) Plans(ctx context.Context, a Actor, p Page, active string, platform bool) (List[Plan], error) {
	out := List[Plan]{Items: []Plan{}, Page: p.Page, Limit: p.Limit}
	if err := ValidatePage(p); err != nil {
		return out, err
	}
	f := bson.M{"isActive": true}
	if platform {
		if _, err := s.platformUser(ctx, a); err != nil {
			return out, err
		}
		f = bson.M{}
		if active != "" {
			if active != "true" && active != "false" {
				return out, apperr.Validation("isActive must be true or false")
			}
			f["isActive"] = active == "true"
		}
	} else if err := allow(a, Admin); err != nil {
		return out, err
	}
	var err error
	out.Total, err = s.rawIdentity("platformPlans").CountDocuments(ctx, f)
	if err != nil {
		return out, err
	}
	cur, err := s.rawIdentity("platformPlans").Find(ctx, f, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "planId", Value: -1}}).SetSkip(int64(p.Page-1)*int64(p.Limit)).SetLimit(int64(p.Limit)))
	if err != nil {
		return out, err
	}
	defer cur.Close(ctx)
	err = cur.All(ctx, &out.Items)
	return out, err
}
func (s *Store) ArchivePlan(ctx context.Context, a Actor, id string) error {
	return s.platformTx(ctx, a, func(sc mongo.SessionContext) error {
		res, err := s.rawIdentity("platformPlans").UpdateOne(sc, bson.M{"planId": id, "isActive": true}, bson.M{"$set": bson.M{"isActive": false, "updatedAt": s.now().UTC()}, "$inc": bson.M{"revision": 1}})
		if err != nil {
			return err
		}
		if res.MatchedCount == 0 {
			return apperr.NotFound()
		}
		return s.platformEvent(sc, a, "planArchived", id, "")
	})
}
func (s *Store) registration(ctx context.Context, account BusinessAccount) (BusinessRegistration, error) {
	out := BusinessRegistration{AccountID: account.AccountID, BusinessProfileInput: account.BusinessProfileInput, IsActive: account.IsActive, CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt, StatusReason: account.StatusReason, Subscription: subscriptionAccess(account, s.now())}
	projection := bson.M{"userId": 1, "name": 1, "username": 1, "email": 1, "phone": 1}
	err := s.rawIdentity("users").FindOne(ctx, bson.M{"accountId": account.AccountID, "userId": account.OwnerUserID, "role": Admin}, options.FindOne().SetProjection(projection)).Decode(&out.Owner)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return out, nil
	}
	return out, err
}
func (s *Store) Businesses(ctx context.Context, a Actor, p Page, active string) (List[BusinessRegistration], error) {
	out := List[BusinessRegistration]{Items: []BusinessRegistration{}, Page: p.Page, Limit: p.Limit}
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
	out.Total, err = s.rawIdentity("accounts").CountDocuments(ctx, f)
	if err != nil {
		return out, err
	}
	cur, err := s.rawIdentity("accounts").Find(ctx, f, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "accountId", Value: -1}}).SetSkip(int64(p.Page-1)*int64(p.Limit)).SetLimit(int64(p.Limit)))
	if err != nil {
		return out, err
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var account BusinessAccount
		if err := cur.Decode(&account); err != nil {
			return out, err
		}
		registration, err := s.registration(ctx, account)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, registration)
	}
	return out, cur.Err()
}
func (s *Store) SetBusinessStatus(ctx context.Context, a Actor, id string, in BusinessStatusInput) (BusinessRegistration, error) {
	var out BusinessRegistration
	if !database.ValidAccountID(id) || in.IsActive == nil {
		return out, apperr.Validation("A valid business and isActive are required")
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		return out, apperr.Validation("A reason is required for changing business access")
	}
	if err := validText(in.Reason); err != nil {
		return out, err
	}
	err := s.platformTx(ctx, a, func(sc mongo.SessionContext) error {
		var account BusinessAccount
		err := s.rawIdentity("accounts").FindOneAndUpdate(sc, bson.M{"accountId": id}, bson.M{"$set": bson.M{"isActive": *in.IsActive, "statusReason": in.Reason, "updatedAt": s.now().UTC()}, "$inc": bson.M{"revision": 1}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&account)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return apperr.NotFound()
		}
		if err != nil {
			return err
		}
		out, err = s.registration(sc, account)
		if err != nil {
			return err
		}
		kind := "businessDeactivated"
		if *in.IsActive {
			kind = "businessReactivated"
		}
		return s.platformEvent(sc, a, kind, id, in.Reason)
	})
	return out, err
}
