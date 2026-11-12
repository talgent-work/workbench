package infrastructure

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/account/application"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

const (
	mysqlDeadlock    = 1213
	signingAlgorithm = "ES256"
	// Each replica rereads the key set at most this often, so a key another
	// replica provisioned is used here within the publication overlap.
	keyCacheTTL = 30 * time.Second
)

// SigningKeyConfig is the platform's key policy.
type SigningKeyConfig struct {
	Issuer   string
	Audience string
	// MasterKey seals private keys at rest; 32 bytes for AES-256-GCM.
	MasterKey []byte
	// Overlap is how long a new key is published before it signs, so the
	// gateway's cached key set already holds it.
	Overlap time.Duration
	// RotationInterval is how long a key signs before its successor takes over.
	RotationInterval time.Duration
	// LongestTokenLifetime keeps a retiring key published until the last token
	// it signed expires, whichever Principal it spoke for (Keys outlive their
	// tokens).
	LongestTokenLifetime time.Duration
	// VerificationRetention keeps a retired key for verifying sign-out of
	// sessions it issued for, up to the absolute session lifetime.
	VerificationRetention time.Duration
	Now                   func() time.Time
}

func (c SigningKeyConfig) validate() error {
	switch {
	case strings.TrimSpace(c.Issuer) == "" || strings.TrimSpace(c.Audience) == "":
		return errors.New("account signing keys: issuer and audience are required")
	case len(c.MasterKey) != 32:
		return errors.New("account signing keys: master key must be 32 bytes")
	case c.Overlap <= 0 || c.LongestTokenLifetime <= 0 || c.RotationInterval <= 2*c.Overlap:
		return errors.New("account signing keys: rotation interval must exceed twice the overlap, and lifetimes must be positive")
	}
	return nil
}

// SigningKeys signs Identity Tokens with keys stored in signing_key so every
// replica signs and verifies with the same set.
type SigningKeys struct {
	engine *xorm.Engine
	cfg    SigningKeyConfig
	sealer keySealer
	now    func() time.Time

	mu       sync.Mutex
	keys     []storedKey
	loadedAt time.Time
}

type storedKey struct {
	id          string
	generation  uint32
	activatesAt time.Time
	retiresAt   time.Time
	private     jwk.Key
	public      jwk.Key
}

var _ application.IdentityTokenPort = (*SigningKeys)(nil)

func NewSigningKeys(engine *xorm.Engine, cfg SigningKeyConfig) (*SigningKeys, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	sealer, err := newKeySealer(cfg.MasterKey)
	if err != nil {
		return nil, err
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &SigningKeys{engine: engine, cfg: cfg, sealer: sealer, now: now}, nil
}

func (s *SigningKeys) Sign(ctx context.Context, claims domain.IdentityClaims) (string, error) {
	key, err := s.signingKey(ctx)
	if err != nil {
		return "", err
	}
	builder := jwt.NewBuilder().
		Issuer(s.cfg.Issuer).
		Subject(claims.Subject).
		Audience([]string{s.cfg.Audience}).
		IssuedAt(claims.IssuedAt).
		Expiration(claims.ExpiresAt).
		JwtID(uuid.NewString()).
		Claim("principal", string(claims.Principal))
	switch claims.Principal {
	case domain.PrincipalUser:
		builder = builder.
			Claim("sid", claims.SessionID).
			Claim("preferred_username", claims.Profile.Username).
			Claim("email", claims.Profile.Email).
			Claim("name", claims.Profile.DisplayName).
			Claim("picture", claims.Profile.AvatarURL).
			Claim("roles", []string{})
	case domain.PrincipalAgent:
		builder = withAgentClaims(builder, claims.Work)
	case domain.PrincipalApplication:
		// An Application's token states only who it is.
	default:
		return "", fmt.Errorf("account signing keys: unknown principal %q", claims.Principal)
	}
	token, err := builder.Build()
	if err != nil {
		return "", err
	}
	signed, err := jwt.Sign(token, jwt.WithKey(jwa.ES256(), key.private))
	if err != nil {
		return "", oops.With("operation", "identity_token.sign").With("kid", key.id).Wrap(err)
	}
	return string(signed), nil
}

// withAgentClaims adds an agent's fixed facts; a fact the Work lacks is left
// out, so the gateway writes no header for it.
func withAgentClaims(builder *jwt.Builder, work domain.WorkFacts) *jwt.Builder {
	for _, claim := range []struct{ name, value string }{
		{"project_id", work.ProjectID},
		{"agent_id", work.AgentID},
		{"purpose", work.Purpose},
		{"work_subject_kind", work.Subject.Kind},
		{"work_subject_id", work.Subject.ID},
		{"for", string(work.RepresentedPerson)},
		{"application_id", string(work.OwningApplication)},
	} {
		if claim.value != "" {
			builder = builder.Claim(claim.name, claim.value)
		}
	}
	if labels := work.WorkLabels.Map(); labels != nil {
		builder = builder.Claim("work_labels", labels)
	}
	return builder
}

// signingKey is the newest key whose activation has passed. Before the first
// rotation has run anywhere, it provisions the first key itself.
func (s *SigningKeys) signingKey(ctx context.Context) (storedKey, error) {
	for attempt := 0; attempt < 2; attempt++ {
		keys, err := s.cached(ctx, attempt > 0)
		if err != nil {
			return storedKey{}, err
		}
		now := s.now()
		for i := len(keys) - 1; i >= 0; i-- {
			if !keys[i].activatesAt.After(now) {
				return keys[i], nil
			}
		}
		if err := s.RotateKeys(ctx); err != nil {
			return storedKey{}, err
		}
	}
	return storedKey{}, errors.New("account signing keys: no active signing key")
}

func (s *SigningKeys) ReferenceSession(ctx context.Context, token string) (application.SessionReference, error) {
	for attempt := 0; attempt < 2; attempt++ {
		keys, err := s.cached(ctx, attempt > 0)
		if err != nil {
			return application.SessionReference{}, err
		}
		set, err := publicSet(keys, func(storedKey) bool { return true })
		if err != nil {
			return application.SessionReference{}, err
		}
		// Expiry is not checked: an expired token still names the session a
		// device signs out of. Signature and issuer are.
		parsed, err := jwt.Parse([]byte(token), jwt.WithKeySet(set), jwt.WithValidate(false))
		if err != nil {
			continue
		}
		return referenceFromToken(parsed, s.cfg.Issuer)
	}
	return application.SessionReference{}, application.ErrIdentityTokenInvalid
}

func referenceFromToken(token jwt.Token, issuer string) (application.SessionReference, error) {
	tokenIssuer, _ := token.Issuer()
	subject, _ := token.Subject()
	var sessionID, principal string
	if tokenIssuer != issuer || token.Get("sid", &sessionID) != nil || token.Get("principal", &principal) != nil || principal != string(domain.PrincipalUser) {
		return application.SessionReference{}, application.ErrIdentityTokenInvalid
	}
	id, err := domain.ParseAccountID(subject)
	if err != nil || sessionID == "" {
		return application.SessionReference{}, application.ErrIdentityTokenInvalid
	}
	return application.SessionReference{Subject: id, SessionID: sessionID}, nil
}

// PublishedKeys lists keys provisioned for signing and those whose tokens may
// still be valid.
func (s *SigningKeys) PublishedKeys(ctx context.Context) ([]byte, error) {
	keys, err := s.cached(ctx, false)
	if err != nil {
		return nil, err
	}
	now := s.now()
	set, err := publicSet(keys, func(key storedKey) bool { return key.retiresAt.IsZero() || now.Before(key.retiresAt) })
	if err != nil {
		return nil, err
	}
	return json.Marshal(set)
}

func publicSet(keys []storedKey, include func(storedKey) bool) (jwk.Set, error) {
	set := jwk.NewSet()
	for _, key := range keys {
		if !include(key) {
			continue
		}
		if err := set.AddKey(key.public); err != nil {
			return nil, err
		}
	}
	return set, nil
}

// RotateKeys provisions the first key, or the successor of the signing key
// once its interval is nearly over, published one overlap before it signs.
// The successor's predecessor retires one longest token lifetime after the
// handover.
// Concurrent replicas race on the unique generation; the loser does nothing.
func (s *SigningKeys) RotateKeys(ctx context.Context) error {
	keys, err := s.load(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	defer s.invalidate()
	if err := s.forgetRetired(ctx, keys, now); err != nil {
		return err
	}
	if len(keys) == 0 {
		return s.provision(ctx, 1, now, nil)
	}
	newest := keys[len(keys)-1]
	if newest.activatesAt.After(now) || now.Before(newest.activatesAt.Add(s.cfg.RotationInterval-s.cfg.Overlap)) {
		return nil
	}
	return s.provision(ctx, newest.generation+1, now.Add(s.cfg.Overlap), &newest)
}

func (s *SigningKeys) provision(ctx context.Context, generation uint32, activatesAt time.Time, predecessor *storedKey) error {
	row, err := s.newKeyRow(generation, activatesAt)
	if err != nil {
		return err
	}
	tx := s.engine.NewSession().Context(ctx)
	defer tx.Close()
	if err := tx.Begin(); err != nil {
		return oops.With("operation", "signing_key.begin").Wrap(err)
	}
	if _, err := tx.Insert(row); err != nil {
		_ = tx.Rollback()
		// Another replica provisioned this generation, or won the lock race
		// for it; either way one successor exists or the next round adds it.
		var conflict *mysql.MySQLError
		if errors.As(err, &conflict) && (conflict.Number == mysqlDuplicateEntry || conflict.Number == mysqlDeadlock) {
			return nil
		}
		return oops.With("operation", "signing_key.insert").With("generation", generation).Wrap(err)
	}
	if predecessor != nil {
		retiresAt := activatesAt.Add(s.cfg.LongestTokenLifetime)
		if _, err := tx.Exec(
			"UPDATE signing_key SET retires_at = ?, updated_at = ?, version = version + 1 WHERE id = ? AND retires_at = 0 AND deleted_at = 0",
			retiresAt.UnixMilli(), row.CreatedAt, predecessor.id,
		); err != nil {
			_ = tx.Rollback()
			return oops.With("operation", "signing_key.retire").With("kid", predecessor.id).Wrap(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return oops.With("operation", "signing_key.commit").Wrap(err)
	}
	return nil
}

func (s *SigningKeys) newKeyRow(generation uint32, activatesAt time.Time) (*signingKeyDO, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	public, err := jwk.Import(&private.PublicKey)
	if err != nil {
		return nil, err
	}
	if err := describeKey(public, id.String()); err != nil {
		return nil, err
	}
	publicJSON, err := json.Marshal(public)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return nil, err
	}
	sealed, err := s.sealer.seal(der, id.String())
	if err != nil {
		return nil, err
	}
	now := s.now().UnixMilli()
	return &signingKeyDO{
		ID:               id.String(),
		Generation:       generation,
		Algorithm:        signingAlgorithm,
		PublicJWK:        string(publicJSON),
		SealedPrivateKey: sealed,
		ActivatesAt:      activatesAt.UnixMilli(),
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}, nil
}

func describeKey(key jwk.Key, kid string) error {
	if err := key.Set(jwk.KeyIDKey, kid); err != nil {
		return err
	}
	if err := key.Set(jwk.AlgorithmKey, jwa.ES256()); err != nil {
		return err
	}
	return key.Set(jwk.KeyUsageKey, "sig")
}

// forgetRetired removes keys no session they issued for can still present.
// It reads the few candidates without locking and removes each by id, so
// replicas sweeping together do not lock ranges against each other.
func (s *SigningKeys) forgetRetired(ctx context.Context, keys []storedKey, now time.Time) error {
	cutoff := now.Add(-s.cfg.VerificationRetention)
	for _, key := range keys {
		if key.retiresAt.IsZero() || !key.retiresAt.Before(cutoff) {
			continue
		}
		if _, err := s.engine.Context(ctx).Exec(
			"UPDATE signing_key SET deleted_at = ?, updated_at = ?, version = version + 1 WHERE id = ? AND deleted_at = 0",
			now.UnixMilli(), now.UnixMilli(), key.id,
		); err != nil {
			return oops.With("operation", "signing_key.forget_retired").With("kid", key.id).Wrap(err)
		}
	}
	return nil
}

func (s *SigningKeys) cached(ctx context.Context, force bool) ([]storedKey, error) {
	s.mu.Lock()
	if !force && s.keys != nil && s.now().Sub(s.loadedAt) < keyCacheTTL {
		keys := s.keys
		s.mu.Unlock()
		return keys, nil
	}
	s.mu.Unlock()
	keys, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.keys, s.loadedAt = keys, s.now()
	s.mu.Unlock()
	return keys, nil
}

func (s *SigningKeys) invalidate() {
	s.mu.Lock()
	s.keys = nil
	s.mu.Unlock()
}

func (s *SigningKeys) load(ctx context.Context) ([]storedKey, error) {
	var rows []signingKeyDO
	if err := s.engine.Context(ctx).
		Cols("id", "generation", "algorithm", "public_jwk", "sealed_private_key", "activates_at", "retires_at").
		Where("deleted_at = 0").
		OrderBy("generation").
		Find(&rows); err != nil {
		return nil, oops.With("operation", "signing_key.load").Wrap(err)
	}
	keys := make([]storedKey, 0, len(rows))
	for i := range rows {
		key, err := s.restore(&rows[i])
		if err != nil {
			return nil, oops.With("operation", "signing_key.restore").With("kid", rows[i].ID).Wrap(err)
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func (s *SigningKeys) restore(row *signingKeyDO) (storedKey, error) {
	if row.Algorithm != signingAlgorithm {
		return storedKey{}, fmt.Errorf("unsupported signing algorithm %q", row.Algorithm)
	}
	der, err := s.sealer.open(row.SealedPrivateKey, row.ID)
	if err != nil {
		return storedKey{}, err
	}
	raw, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return storedKey{}, err
	}
	privateKey, ok := raw.(*ecdsa.PrivateKey)
	if !ok {
		return storedKey{}, errors.New("signing key is not ECDSA")
	}
	private, err := jwk.Import(privateKey)
	if err != nil {
		return storedKey{}, err
	}
	if err := describeKey(private, row.ID); err != nil {
		return storedKey{}, err
	}
	public, err := jwk.ParseKey([]byte(row.PublicJWK))
	if err != nil {
		return storedKey{}, err
	}
	return storedKey{
		id:          row.ID,
		generation:  row.Generation,
		activatesAt: fromMillis(row.ActivatesAt),
		retiresAt:   fromMillis(row.RetiresAt),
		private:     private,
		public:      public,
	}, nil
}
