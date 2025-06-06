package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func HashPassword(password string) (string, error) {
	s, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(s), err
}

func CheckPasswordHash(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func MakeJWT(userID uuid.UUID, tokenSecret string, expiresIn time.Duration) (string, error) {
	claims := &jwt.RegisteredClaims{
		Issuer:    "chirpy",
		IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
		ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(expiresIn)),
		Subject:   userID.String(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	sig, err := token.SignedString([]byte(tokenSecret))
	if err != nil {
		return "", err
	}
	return sig, nil
}

func ValidateJWT(tokenString, tokenSecret string) (uuid.UUID, error) {
	token, err := jwt.ParseWithClaims(tokenString, &jwt.RegisteredClaims{}, func(t *jwt.Token) (interface{}, error) {
		return []byte(tokenSecret), nil
	})
	if err != nil {
		return uuid.UUID{}, err
	} else if subj, ok := token.Claims.GetSubject(); ok == nil {
		return uuid.Parse(subj)
	} else {
		return uuid.UUID{}, ok
	}
}

func GetBearerToken(headers http.Header) (string, error) {
	authorization := headers.Get("Authorization")
	if authorization == "" {
		return "", fmt.Errorf("authorization not found in header")
	}
	if token, ok := strings.CutPrefix(authorization, "Bearer "); ok {
		return token, nil
	} else {
		return "", fmt.Errorf("bearer not found in header")
	}
}

func MakeRefreshToken() (string, error) {
	randBytes := make([]byte, 32)
	_, _ = rand.Read(randBytes)
	return hex.EncodeToString(randBytes), nil
}

func GetAPIKey(headers http.Header) (string, error) {
	authorization := headers.Get("Authorization")
	if authorization == "" {
		return "", fmt.Errorf("authorization not found in header")
	}
	if token, ok := strings.CutPrefix(authorization, "ApiKey "); ok {
		return token, nil
	} else {
		return "", fmt.Errorf("ApiKey not found in header")
	}
}
