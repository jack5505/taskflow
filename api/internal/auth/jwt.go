package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims — содержимое access-токена: только идентификатор пользователя.
// Роли и активность не кладём: они меняются чаще, чем живёт токен, и читаются из базы на каждом запросе.
type Claims struct {
	jwt.RegisteredClaims
}

var errInvalidToken = errors.New("access-токен недействителен")

// signAccess выпускает HS256-токен на ttl.
func signAccess(secret []byte, userID uuid.UUID, now time.Time, ttl time.Duration) (string, error) {
	claims := Claims{RegisteredClaims: jwt.RegisteredClaims{
		Subject:   userID.String(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		ID:        uuid.NewString(),
	}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
}

// parseAccess проверяет подпись и срок; возвращает id пользователя.
func parseAccess(secret []byte, token string, now time.Time) (uuid.UUID, error) {
	var claims Claims
	parsed, err := jwt.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("неожиданный алгоритм %v", t.Header["alg"])
		}
		return secret, nil
	}, jwt.WithTimeFunc(func() time.Time { return now }), jwt.WithExpirationRequired())
	if err != nil || !parsed.Valid {
		return uuid.Nil, errInvalidToken
	}
	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, errInvalidToken
	}
	return id, nil
}

// newRefreshToken — 32 случайных байта в base64url; в базу идёт только hashToken(token).
func newRefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
