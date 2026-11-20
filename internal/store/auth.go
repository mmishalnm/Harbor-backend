package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/crypto/bcrypt"
	"harbor/internal/apperr"
	"harbor/internal/database"
)

type Claims struct {
	AccountID string `json:"accountId"`
	Subject   string `json:"sub"`
	Role      string `json:"role"`
	Expires   int64  `json:"exp"`
	Issued    int64  `json:"iat"`
	Issuer    string `json:"iss"`
}
type LoginResult struct {
	Access      *SubscriptionAccess `json:"access,omitempty"`
	AccessToken string              `json:"accessToken"`
	TokenType   string              `json:"tokenType"`
	ExpiresIn   int                 `json:"expiresIn"`
	User        User                `json:"user"`
	Account     *BusinessAccount    `json:"account"`
}

var invalidAuth = apperr.New(401, "UNAUTHORIZED", "Invalid credentials or expired token")

func (s *Store) token(u User) (string, error) {
	now := s.now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(Claims{AccountID: u.AccountID, Subject: u.UserID, Role: u.Role, Expires: now.Add(8 * time.Hour).Unix(), Issued: now.Unix(), Issuer: "harbor"})
	if err != nil {
		return "", err
	}
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	h := hmac.New(sha256.New, s.secret)
	h.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil)), nil
}
func (s *Store) verify(token string) (Claims, error) {
	var c Claims
	if len(token) > 4096 {
		return c, invalidAuth
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return c, invalidAuth
	}
	hdr, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return c, invalidAuth
	}
	var h struct {
		Alg  string `json:"alg"`
		Type string `json:"typ"`
	}
	if json.Unmarshal(hdr, &h) != nil || h.Alg != "HS256" || h.Type != "JWT" {
		return c, invalidAuth
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return c, invalidAuth
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return c, invalidAuth
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(body, &c) != nil {
		return c, invalidAuth
	}
	if c.Subject == "" || c.Issuer != "harbor" || c.Expires <= s.now().Unix() || c.Issued > s.now().Unix()+30 || c.Expires <= c.Issued {
		return c, invalidAuth
	}
	platform := c.Role == SuperAdmin && c.AccountID == platformAccount
	business := database.ValidAccountID(c.AccountID) && allow(Actor{Role: c.Role}, Admin, Manager, WarehouseStaff, DeliveryPartner) == nil
	if !platform && !business {
		return c, invalidAuth
	}
	return c, nil
}
func (s *Store) Authenticate(ctx context.Context, token string) (Actor, error) {
	c, err := s.verify(token)
	if err != nil {
		return Actor{}, err
	}
	if c.Role == SuperAdmin {
		a := Actor{Role: SuperAdmin, UserID: c.Subject, AccountID: platformAccount}
		_, err := s.platformUser(ctx, a)
		return a, err
	}
	ctx = database.WithAccount(ctx, c.AccountID)
	var u User
	err = s.col("users").FindOne(ctx, bson.M{"userId": c.Subject, "isActive": true}).Decode(&u)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Actor{}, invalidAuth
	}
	if err != nil {
		return Actor{}, err
	}
	if u.Role != c.Role {
		return Actor{}, invalidAuth
	}
	var account BusinessAccount
	if err := s.col("accounts").FindOne(ctx, bson.M{"accountId": c.AccountID}).Decode(&account); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return Actor{}, invalidAuth
		}
		return Actor{}, err
	}
	if !account.IsActive {
		return Actor{}, subscriptionError(subscriptionAccess(account, s.now()))
	}
	return Actor{AssignedVehicleID: u.AssignedVehicleID, UserID: u.UserID, Role: u.Role, LocationIDs: u.LocationIDs, AccountID: u.AccountID}, nil
}
func (s *Store) Login(ctx context.Context, username, password string) (LoginResult, error) {
	var u User
	out := LoginResult{}
	if len(username) > 100 || len(password) > 72 || username == "" || password == "" {
		return out, invalidAuth
	}
	err := s.rawIdentity("users").FindOne(ctx, bson.M{"username": strings.ToLower(strings.TrimSpace(username)), "isActive": true}).Decode(&u)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return out, err
	}
	hash := u.PasswordHash
	if hash == "" {
		hash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || u.UserID == "" {
		return out, invalidAuth
	}
	if u.Role == SuperAdmin && u.AccountID == platformAccount {
		return s.loginResult(u, BusinessAccount{})
	}
	if !database.ValidAccountID(u.AccountID) {
		return out, invalidAuth
	}
	if allow(Actor{Role: u.Role}, Admin, Manager, WarehouseStaff, DeliveryPartner) != nil {
		return out, invalidAuth
	}
	var account BusinessAccount
	err = s.col("accounts").FindOne(database.WithAccount(ctx, u.AccountID), bson.M{}).Decode(&account)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return out, invalidAuth
	}
	if err != nil {
		return out, err
	}
	if !account.IsActive {
		return out, subscriptionError(subscriptionAccess(account, s.now()))
	}
	return s.loginResult(u, account)
}
func (s *Store) Bootstrap(ctx context.Context, username, password string) error {
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
			if u.Role != Admin || !u.IsActive || !database.ValidAccountID(u.AccountID) {
				return apperr.Conflict("BOOTSTRAP_CONFLICT", "Bootstrap admin must belong to an active business account; migrate legacy data first")
			}
			return nil
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		_, err = s.registerAccount(sc, SignupInput{Name: "Administrator", Username: username, BusinessProfileInput: BusinessProfileInput{BusinessName: "Harbor Business", Address: BusinessAddress{Country: "IN"}}}, string(hash))
		return err
	})
}

type CreateUserInput struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
}

func (s *Store) CreateUser(ctx context.Context, a Actor, in CreateUserInput) (User, error) {
	var out User
	if err := validName(in.Name); err != nil {
		return out, err
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if in.Email != "" && !validEmail(in.Email) {
		return out, apperr.Validation("email must be a valid email address")
	}
	if len(in.Phone) > 30 {
		return out, apperr.Validation("phone must be at most 30 bytes")
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	if in.Username == "" || len(in.Username) > 100 || strings.ContainsAny(in.Username, " \t\r\n") {
		return out, apperr.Validation("username is required, at most 100 bytes, without whitespace")
	}
	if len(in.Password) < 12 || len(in.Password) > 72 {
		return out, apperr.Validation("Password must contain 12 to 72 bytes")
	}
	if allow(Actor{Role: in.Role}, Manager, WarehouseStaff, DeliveryPartner) != nil {
		return out, apperr.Validation("Staff role must be manager, warehouseStaff, or deliveryPartner")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return out, err
	}
	err = s.tx(ctx, a, []string{Admin}, func(sc mongo.SessionContext, _ Actor) error {
		if in.Role == DeliveryPartner {
			if err := s.checkCapacity(sc, "partner", ""); err != nil {
				return err
			}
		}
		if err := s.identityAvailable(sc, in.Username, in.Email); err != nil {
			if apperr.Public(err).Code == "LOGIN_EXISTS" {
				return apperr.Conflict("USERNAME_EXISTS", "Username or email already exists")
			}
			return err
		}
		id, err := s.next(sc, "user", "USR-")
		if err != nil {
			return err
		}
		out = User{Base: s.base(), AccountID: a.AccountID, Email: in.Email, Phone: in.Phone, UserID: id, Name: strings.TrimSpace(in.Name), Username: in.Username, PasswordHash: string(hash), Role: in.Role, LocationIDs: []string{}}
		return insert(sc, s.col("users"), out)
	})
	return out, err
}
func (s *Store) AssignLocations(ctx context.Context, a Actor, userID string, ids []string) (User, error) {
	var out User
	if ids == nil {
		return out, apperr.Validation("locationIds must be an array; use [] to clear assignments")
	}
	if len(ids) > 200 {
		return out, apperr.Validation("At most 200 location assignments are allowed")
	}
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, _ Actor) error {
		if err := s.lock(sc, "users", "userId", userID, &out); err != nil {
			return err
		}
		if out.Role != WarehouseStaff && out.Role != DeliveryPartner {
			return apperr.Validation("Only warehouseStaff or deliveryPartner can have location assignments")
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				return apperr.Validation("Duplicate locationId")
			}
			seen[id] = true
			if _, err := s.storage(sc, id); err != nil {
				return err
			}
		}
		if ids == nil {
			ids = []string{}
		}
		out.LocationIDs = ids
		return s.update(sc, "users", "userId", userID, bson.M{"locationIds": ids})
	})
	return out, err
}
