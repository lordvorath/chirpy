package auth

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCheckPasswordHash(t *testing.T) {
	// First, we need to create some hashed passwords for testing
	password1 := "correctPassword123!"
	password2 := "anotherPassword456!"
	hash1, _ := HashPassword(password1)
	hash2, _ := HashPassword(password2)

	tests := []struct {
		name     string
		password string
		hash     string
		wantErr  bool
	}{
		{
			name:     "Correct password",
			password: password1,
			hash:     hash1,
			wantErr:  false,
		},
		{
			name:     "Incorrect password",
			password: "wrongPassword",
			hash:     hash1,
			wantErr:  true,
		},
		{
			name:     "Password doesn't match different hash",
			password: password1,
			hash:     hash2,
			wantErr:  true,
		},
		{
			name:     "Empty password",
			password: "",
			hash:     hash1,
			wantErr:  true,
		},
		{
			name:     "Invalid hash",
			password: password1,
			hash:     "invalidhash",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckPasswordHash(tt.hash, tt.password)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckPasswordHash() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckJWT(t *testing.T) {
	secret := "Dw/G:+@%VR[a$LV,D4L{5+(4I}+zf+ER"
	userid := uuid.New()
	jwt1, err := MakeJWT(userid, secret, time.Hour)
	if err != nil {
		t.Errorf("MakeJWT error: %s", err)
	}
	id, err := ValidateJWT(jwt1, secret)
	if err != nil {
		t.Errorf("ValidateJWT error: %s", err)
	}
	if id != userid {
		t.Errorf("UUIDs don't match:\nog: %v\nto: %v\n", userid, id)
	}

}

func TestGetBearerToken(t *testing.T) {
	tokenString := "myTokenString"
	jwt1 := "Bearer " + tokenString
	header := http.Header{}
	header.Set("Authorization", jwt1)
	ts, err := GetBearerToken(header)
	if err != nil {
		t.Errorf("failed to retrieve token string: %s", err)
	}
	if ts != tokenString {
		t.Errorf("token string mismatch:\ntjwt: %s\ntok: %s", jwt1, ts)
	}
}
