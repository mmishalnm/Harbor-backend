package store

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"
	"harbor/internal/apperr"
	"harbor/internal/database"
)

type BusinessAddress struct {
	Line1      string `json:"line1" bson:"line1"`
	Line2      string `json:"line2" bson:"line2"`
	City       string `json:"city" bson:"city"`
	State      string `json:"state" bson:"state"`
	PostalCode string `json:"postalCode" bson:"postalCode"`
	Country    string `json:"country" bson:"country"`
}
type BusinessProfileInput struct {
	BusinessName  string          `json:"businessName" bson:"businessName"`
	BusinessType  string          `json:"businessType" bson:"businessType"`
	BusinessEmail string          `json:"businessEmail" bson:"businessEmail"`
	BusinessPhone string          `json:"businessPhone" bson:"businessPhone"`
	Address       BusinessAddress `json:"address" bson:"address"`
}
type BusinessAccount struct {
	OrderSettings        OrderSettings `json:"orderSettings" bson:"orderSettings"`
	Base                 `bson:",inline"`
	AccountID            string `json:"accountId" bson:"accountId"`
	OwnerUserID          string `json:"ownerUserId" bson:"ownerUserId"`
	BusinessProfileInput `bson:",inline"`
}
type SignupInput struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Password string `json:"password"`
	BusinessProfileInput
}
type SessionProfile struct {
	User    User            `json:"user"`
	Account BusinessAccount `json:"account"`
}

func validEmail(value string) bool {
	parsed, err := mail.ParseAddress(value)
	return err == nil && parsed.Address == value && len(value) <= 254 && strings.Contains(value, "@")
}
func validateCredentials(username, password string) error {
	if username == "" || len(username) > 100 || strings.ContainsAny(username, " \t\r\n") {
		return apperr.Validation("username is required, at most 100 bytes, without whitespace")
	}
	if len(password) < 12 || len(password) > 72 {
		return apperr.Validation("Password must contain 12 to 72 bytes")
	}
	return nil
}
func validateBusiness(in BusinessProfileInput) error {
	if err := validName(in.BusinessName); err != nil {
		return apperr.Validation("businessName is required and must be at most 200 bytes")
	}
	if len(in.BusinessType) > 100 {
		return apperr.Validation("businessType must be at most 100 bytes")
	}
	if in.BusinessEmail != "" && !validEmail(in.BusinessEmail) {
		return apperr.Validation("businessEmail must be a valid email address")
	}
	if len(in.BusinessPhone) > 30 {
		return apperr.Validation("businessPhone must be at most 30 bytes")
	}
	for _, v := range []string{in.Address.Line1, in.Address.Line2, in.Address.City, in.Address.State} {
		if len(v) > 200 {
			return apperr.Validation("Address fields must be at most 200 bytes")
		}
	}
	if len(in.Address.PostalCode) > 20 || (in.Address.Country != "" && len(in.Address.Country) != 2) {
		return apperr.Validation("postalCode must be at most 20 bytes and country must be a two-letter code")
	}
	return nil
}
func normalizeSignup(in SignupInput) (SignupInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.BusinessName = strings.TrimSpace(in.BusinessName)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	if in.Username == "" {
		in.Username = in.Email
	}
	if err := validName(in.Name); err != nil {
		return in, err
	}
	if !validEmail(in.Email) {
		return in, apperr.Validation("email must be a valid email address")
	}
	if strings.TrimSpace(in.Phone) == "" || len(in.Phone) > 30 {
		return in, apperr.Validation("phone is required and must be at most 30 bytes")
	}
	if err := validateCredentials(in.Username, in.Password); err != nil {
		return in, err
	}
	if in.BusinessEmail == "" {
		in.BusinessEmail = in.Email
	}
	if in.BusinessPhone == "" {
		in.BusinessPhone = in.Phone
	}
	if in.Address.Country == "" {
		in.Address.Country = "IN"
	}
	return in, validateBusiness(in.BusinessProfileInput)
}
func scopedSession(sc mongo.SessionContext, accountID string) mongo.SessionContext {
	return mongo.NewSessionContext(database.WithAccount(sc, accountID), mongo.SessionFromContext(sc))
}

// rawIdentity is intentionally confined to login, registration, bootstrap and
// offline migration. Business operations must use the scoped collection API.
func (s *Store) rawIdentity(name string) *mongo.Collection { return s.db.Mongo.Collection(name) }
func (s *Store) identityAvailable(sc mongo.SessionContext, username, email string) error {
	query := bson.M{"username": username}
	if email != "" {
		query = bson.M{"$or": bson.A{query, bson.M{"email": email}}}
	}
	n, err := s.rawIdentity("users").CountDocuments(sc, query)
	if err != nil {
		return err
	}
	if n > 0 {
		return apperr.Conflict("LOGIN_EXISTS", "This username or email is already registered")
	}
	return nil
}
func (s *Store) nextAccount(sc mongo.SessionContext) (string, error) {
	var c struct {
		Seq int64 `bson:"seq"`
	}
	err := s.rawIdentity("platformCounters").FindOneAndUpdate(sc, bson.M{"counterName": "account"}, bson.M{"$inc": bson.M{"seq": 1}}, options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&c)
	return fmt.Sprintf("ACC-%06d", c.Seq), err
}
func (s *Store) registerAccount(sc mongo.SessionContext, in SignupInput, hash string) (SessionProfile, error) {
	var out SessionProfile
	if err := s.identityAvailable(sc, in.Username, in.Email); err != nil {
		return out, err
	}
	aid, err := s.nextAccount(sc)
	if err != nil {
		return out, err
	}
	sc = scopedSession(sc, aid)
	uid, err := s.next(sc, "user", "USR-")
	if err != nil {
		return out, err
	}
	out.User = User{Base: s.base(), AccountID: aid, UserID: uid, Name: in.Name, Username: in.Username, Email: in.Email, Phone: in.Phone, PasswordHash: hash, Role: Admin, LocationIDs: []string{}}
	out.Account = BusinessAccount{OrderSettings: effectiveOrderSettings(OrderSettings{}), Base: s.base(), AccountID: aid, OwnerUserID: uid, BusinessProfileInput: in.BusinessProfileInput}
	if err := insert(sc, s.col("accounts"), out.Account); err != nil {
		return out, err
	}
	return out, insert(sc, s.col("users"), out.User)
}
func (s *Store) Signup(ctx context.Context, in SignupInput) (LoginResult, error) {
	var result LoginResult
	in, err := normalizeSignup(in)
	if err != nil {
		return result, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return result, err
	}
	var profile SessionProfile
	err = s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		var err error
		profile, err = s.registerAccount(sc, in, string(hash))
		return err
	})
	if err != nil {
		return result, err
	}
	return s.loginResult(profile.User, profile.Account)
}
func (s *Store) loginResult(u User, account BusinessAccount) (LoginResult, error) {
	account.OrderSettings = effectiveOrderSettings(account.OrderSettings)
	token, err := s.token(u)
	return LoginResult{AccessToken: token, TokenType: "Bearer", ExpiresIn: 28800, User: u, Account: account}, err
}
func (s *Store) MyProfile(ctx context.Context, a Actor) (SessionProfile, error) {
	ctx = database.WithAccount(ctx, a.AccountID)
	var out SessionProfile
	err := s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		if err := s.find(sc, "users", "userId", a.UserID, &out.User); err != nil {
			return err
		}
		if !out.User.IsActive || out.User.Role != a.Role {
			return invalidAuth
		}
		return s.find(sc, "accounts", "accountId", a.AccountID, &out.Account)
	})
	return out, err
}
func (s *Store) UpdateBusiness(ctx context.Context, a Actor, in BusinessProfileInput) (BusinessAccount, error) {
	var out BusinessAccount
	if err := validateBusiness(in); err != nil {
		return out, err
	}
	err := s.tx(ctx, a, []string{Admin}, func(sc mongo.SessionContext, _ Actor) error {
		if err := s.update(sc, "accounts", "accountId", a.AccountID, bson.M{"businessName": strings.TrimSpace(in.BusinessName), "businessType": in.BusinessType, "businessEmail": in.BusinessEmail, "businessPhone": in.BusinessPhone, "address": in.Address}); err != nil {
			return err
		}
		return s.find(sc, "accounts", "accountId", a.AccountID, &out)
	})
	return out, err
}

// MigrateLegacyAccount is an explicit, offline ownership migration. It never
// assigns old data to a public signup, edits balances, or renumbers invoices.
func (s *Store) MigrateLegacyAccount(ctx context.Context, ownerUsername, businessName string) (BusinessAccount, error) {
	var out BusinessAccount
	if err := validName(businessName); err != nil {
		return out, err
	}
	if err := s.db.PrepareIdentityIndexes(ctx); err != nil {
		return out, err
	}
	err := s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		var owner User
		if err := s.rawIdentity("users").FindOne(sc, bson.M{"username": strings.ToLower(strings.TrimSpace(ownerUsername)), "role": Admin, "isActive": true}).Decode(&owner); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return apperr.Validation("An existing active admin username is required for legacy migration")
			}
			return err
		}
		if owner.AccountID != "" {
			return apperr.Conflict("ALREADY_MIGRATED", "This administrator already belongs to a business account")
		}
		aid, err := s.nextAccount(sc)
		if err != nil {
			return err
		}
		out = BusinessAccount{OrderSettings: effectiveOrderSettings(OrderSettings{}), Base: s.base(), AccountID: aid, OwnerUserID: owner.UserID, BusinessProfileInput: BusinessProfileInput{BusinessName: businessName, Address: BusinessAddress{Country: "IN"}}}
		if err := insert(scopedSession(sc, aid), s.col("accounts"), out); err != nil {
			return err
		}
		for col := range database.BusinessIDs {
			if _, err := s.rawIdentity(col).UpdateMany(sc, database.LegacyFilter(), bson.M{"$set": bson.M{"accountId": aid}}); err != nil {
				return err
			}
		}
		_, err = s.rawIdentity("trips").UpdateMany(sc, bson.M{"accountId": aid, "warehouseStaffIds": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"warehouseStaffIds": []string{}}})
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	return out, s.db.EnsureIndexes(ctx)
}
