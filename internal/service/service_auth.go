package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"go-kpi-tenders/internal/repository"
)

const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 30 * 24 * time.Hour
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidToken       = errors.New("invalid or expired token")
)

// dummyHash is pre-computed once at startup with the same cost as real password
// hashes. It is used to equalise response timing when the requested user does
// not exist, preventing user-enumeration via timing side-channel.
var dummyHash []byte

func init() {
	h, err := bcrypt.GenerateFromPassword([]byte("dummy-timing-equaliser"), bcrypt.DefaultCost)
	if err != nil {
		panic("auth: failed to generate dummy bcrypt hash: " + err.Error())
	}
	dummyHash = h
}

// Claims is the payload for access tokens.
type Claims struct {
	UserID uuid.UUID `json:"user_id"`
	OrgID  uuid.UUID `json:"org_id"`
	Role   string    `json:"role"`
	jwt.RegisteredClaims
}

// RefreshClaims is the payload for refresh tokens.
type RefreshClaims struct {
	UserID uuid.UUID `json:"user_id"`
	jwt.RegisteredClaims
}

// AuthService handles authentication logic.
type AuthService struct {
	repo          *repository.Queries
	log           *slog.Logger
	accessSecret  []byte
	refreshSecret []byte
}

func NewAuthService(repo *repository.Queries, log *slog.Logger, accessSecret, refreshSecret string) *AuthService {
	return &AuthService{
		repo:          repo,
		log:           log,
		accessSecret:  []byte(accessSecret),
		refreshSecret: []byte(refreshSecret),
	}
}

// Login verifies credentials and returns a signed access/refresh token pair.
// Returns ErrInvalidCredentials on auth failure (same message for missing user
// and wrong password to prevent user enumeration).
// Returns a wrapped internal error on unexpected repository failures.
func (s *AuthService) Login(ctx context.Context, email, password string) (accessToken, refreshToken string, err error) {
	user, repoErr := s.repo.GetUserByEmail(ctx, email)
	if repoErr != nil {
		if errors.Is(repoErr, pgx.ErrNoRows) {
			// Run bcrypt on the dummy hash to equalise response timing so callers
			// cannot enumerate existing emails by measuring latency.
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
			s.log.Warn("login: invalid credentials")
			return "", "", ErrInvalidCredentials
		}
		s.log.Error("login: repository error", slog.String("err", repoErr.Error()))
		return "", "", fmt.Errorf("login: %w", repoErr)
	}

	if err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		s.log.Warn("login: invalid credentials")
		return "", "", ErrInvalidCredentials
	}

	return s.GenerateTokens(user.ID, user.OrganizationID, user.Role)
}

// GenerateTokens creates a new access/refresh token pair for the given user.
func (s *AuthService) GenerateTokens(userID, orgID uuid.UUID, role string) (accessToken, refreshToken string, err error) {
	now := time.Now()

	accessClaims := Claims{
		UserID: userID,
		OrgID:  orgID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	accessToken, err = jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims).SignedString(s.accessSecret)
	if err != nil {
		return "", "", fmt.Errorf("sign access token: %w", err)
	}

	refreshClaims := RefreshClaims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(RefreshTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	refreshToken, err = jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims).SignedString(s.refreshSecret)
	if err != nil {
		return "", "", fmt.Errorf("sign refresh token: %w", err)
	}

	return accessToken, refreshToken, nil
}

// ValidateAccessToken parses and validates an access token, returning its claims.
func (s *AuthService) ValidateAccessToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenStr,
		&Claims{},
		func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, ErrInvalidToken
			}
			return s.accessSecret, nil
		},
		jwt.WithExpirationRequired(),
	)
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// ValidateRefreshToken parses and validates a refresh token, returning its claims.
func (s *AuthService) ValidateRefreshToken(tokenStr string) (*RefreshClaims, error) {
	token, err := jwt.ParseWithClaims(
		tokenStr,
		&RefreshClaims{},
		func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, ErrInvalidToken
			}
			return s.refreshSecret, nil
		},
		jwt.WithExpirationRequired(),
	)
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*RefreshClaims)
	if !ok {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
