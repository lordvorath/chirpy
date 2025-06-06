package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/lordvorath/chirpy/internal/auth"
	"github.com/lordvorath/chirpy/internal/database"
)

type apiConfig struct {
	fileserverHits atomic.Int32
	queries        *database.Queries
	platform       string
	secret         string
	polka_key      string
}

type User struct {
	ID          uuid.UUID `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Email       string    `json:"email"`
	Password    string    `json:"-"`
	IsChirpyRed bool      `json:"is_chirpy_red"`
}

func main() {
	godotenv.Load()
	dbURL := os.Getenv("DB_URL")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal("failed to open db connection")
	}
	const filepathRoot = "."
	const port = "8080"
	apiCfg := apiConfig{
		fileserverHits: atomic.Int32{},
		queries:        database.New(db),
		platform:       os.Getenv("PLATFORM"),
		secret:         os.Getenv("SECRET"),
		polka_key:      os.Getenv("POLKA_KEY"),
	}

	mux := http.NewServeMux()
	mux.Handle("/app/", apiCfg.middlewareMetricsInc(http.StripPrefix("/app", http.FileServer(http.Dir(filepathRoot)))))
	mux.HandleFunc("GET /api/healthz", handlerReadiness)
	mux.HandleFunc("POST /api/users", apiCfg.handlerCreateUser)
	mux.HandleFunc("POST /api/login", apiCfg.handlerLogin)
	mux.HandleFunc("POST /api/refresh", apiCfg.handlerRefresh)
	mux.HandleFunc("POST /api/revoke", apiCfg.handlerRevoke)
	mux.HandleFunc("POST /api/chirps", apiCfg.handlerCreateChirp)
	mux.HandleFunc("GET /api/chirps", apiCfg.handlerGetChirps)
	mux.HandleFunc("GET /api/chirps/{chirpID}", apiCfg.handlerGetChirpByID)
	mux.HandleFunc("DELETE /api/chirps/{chirpID}", apiCfg.handlerDeleteChirp)
	mux.HandleFunc("GET /admin/metrics", apiCfg.handlerMetrics)
	mux.HandleFunc("POST /admin/reset", apiCfg.handlerReset)
	mux.HandleFunc("PUT /api/users", apiCfg.handlerUsers)
	mux.HandleFunc("POST /api/polka/webhooks", apiCfg.handlerUpgradeUser)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	log.Printf("Serving files from %s on port: %s\n", filepathRoot, port)
	log.Fatal(srv.ListenAndServe())
}

func handlerReadiness(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(http.StatusText(http.StatusOK)))
}

func (cfg *apiConfig) handlerMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	htmlContent := fmt.Sprintf(`<html>
  <body>
    <h1>Welcome, Chirpy Admin</h1>
    <p>Chirpy has been visited %d times!</p>
  </body>
</html>`, int(cfg.fileserverHits.Load()))
	w.Write([]byte(htmlContent))
}

func (cfg *apiConfig) handlerReset(w http.ResponseWriter, r *http.Request) {
	if cfg.platform != "dev" {
		respondWithError(w, http.StatusForbidden, "not allowed")
		return
	}
	err := cfg.queries.DeleteAllUsers(r.Context())
	if err != nil {
		log.Printf("failed to delete users: %s", err)
	}
	err = cfg.queries.DeleteAllChirps(r.Context())
	if err != nil {
		log.Printf("failed to delete chirps: %s", err)
	}
	err = cfg.queries.DeleteAllRefreshTokens(r.Context())
	if err != nil {
		log.Printf("failed to delete refresh tokens: %s", err)
	}
	cfg.fileserverHits.Store(0)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Hits reset to 0"))
}

func (cfg *apiConfig) handlerCreateChirp(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Body   string    `json:"body"`
		UserID uuid.UUID `json:"user_id"`
	}
	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Something went wrong: %v", err))
		return
	}
	if len(params.Body) > 140 {
		respondWithError(w, http.StatusBadRequest, "Chirp is too long")
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("Request is missing a JWT: %s", err))
		return
	}
	userid, err := auth.ValidateJWT(token, cfg.secret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("Invalid JWT: %s", err))
		return
	}

	cleaned := make([]string, 0)
	for _, word := range strings.Fields(params.Body) {
		if strings.EqualFold(word, "kerfuffle") ||
			strings.EqualFold(word, "sharbert") ||
			strings.EqualFold(word, "fornax") {
			word = "****"
		}
		cleaned = append(cleaned, word)
	}
	cleaned_string := strings.Join(cleaned, " ")
	newChirpParams := database.CreateChirpParams{
		Body:   cleaned_string,
		UserID: userid,
	}

	newChirp, err := cfg.queries.CreateChirp(r.Context(), newChirpParams)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Failed to create chirp: %v", err))
		return
	}

	respondWithJSON(w, http.StatusCreated, newChirp)
}

func (cfg *apiConfig) handlerGetChirps(w http.ResponseWriter, r *http.Request) {
	chirps, err := cfg.queries.GetAllChirps(r.Context())
	if err != nil {
		respondWithError(w, http.StatusForbidden, fmt.Sprintf("Error retrieving all chirps: %v", err))
		return
	}
	respondWithJSON(w, http.StatusOK, chirps)
}

func (cfg *apiConfig) handlerGetChirpByID(w http.ResponseWriter, r *http.Request) {
	chirpID := r.PathValue("chirpID")
	if chirpID == "" {
		respondWithError(w, http.StatusNotFound, "Malformed request")
		return
	}
	uid, err := uuid.Parse(chirpID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, fmt.Sprintf("Bad chirp UUID: %v", err))
		return
	}
	chirp, err := cfg.queries.GetChirpByID(r.Context(), uid)
	if err != nil {
		respondWithError(w, http.StatusNotFound, fmt.Sprintf("Failed to retrieve chirp: %v", err))
		return
	}
	respondWithJSON(w, http.StatusOK, chirp)

}

func (cfg *apiConfig) handlerCreateUser(w http.ResponseWriter, r *http.Request) {
	reqBody := struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}{}
	err := json.NewDecoder(r.Body).Decode(&reqBody)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Couldn't decode parameters: %s", err))
		return
	}
	userParams := database.CreateUserParams{
		Email:          reqBody.Email,
		HashedPassword: reqBody.Password,
	}
	userParams.HashedPassword, err = auth.HashPassword(reqBody.Password)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Couldn't hash the password: %s", err))
	}
	usr, err := cfg.queries.CreateUser(r.Context(), userParams)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Couldn't create user: %s", err))
		return
	}
	nuser := User{
		ID:          usr.ID,
		CreatedAt:   usr.CreatedAt,
		UpdatedAt:   usr.UpdatedAt,
		Email:       usr.Email,
		IsChirpyRed: usr.IsChirpyRed,
	}
	respondWithJSON(w, http.StatusCreated, nuser)
}

func (cfg *apiConfig) handlerLogin(w http.ResponseWriter, r *http.Request) {
	reqBody := struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	}{}
	err := json.NewDecoder(r.Body).Decode(&reqBody)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Couldn't decode parameters: %s", err))
		return
	}
	usr, err := cfg.queries.GetUserByEmail(r.Context(), reqBody.Email)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't find user")
		return
	}
	err = auth.CheckPasswordHash(usr.HashedPassword, reqBody.Password)
	if err != nil {
		respondWithJSON(w, http.StatusUnauthorized, fmt.Sprintf("Incorrect email or password: %s", err))
		return
	}
	token, err := auth.MakeJWT(usr.ID, cfg.secret, time.Hour)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Couldn't make JWT: %s", err))
		return
	}
	refresh_token, _ := auth.MakeRefreshToken()
	dbtoken, err := cfg.queries.CreateRefreshToken(r.Context(), database.CreateRefreshTokenParams{
		Token:     refresh_token,
		UserID:    usr.ID,
		ExpiresAt: time.Now().Add(time.Hour * 24 * 60),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Couldn't get refresh token: %s", err))
		return
	}
	nuser := struct {
		ID           uuid.UUID `json:"id"`
		CreatedAt    time.Time `json:"created_at"`
		UpdatedAt    time.Time `json:"updated_at"`
		Email        string    `json:"email"`
		Token        string    `json:"token"`
		RefreshToken string    `json:"refresh_token"`
		IsChirpyRed  bool      `json:"is_chirpy_red"`
	}{
		ID:           usr.ID,
		Email:        usr.Email,
		CreatedAt:    usr.CreatedAt,
		UpdatedAt:    usr.UpdatedAt,
		Token:        token,
		RefreshToken: dbtoken.Token,
		IsChirpyRed:  usr.IsChirpyRed,
	}
	respondWithJSON(w, http.StatusOK, nuser)
}

func (cfg *apiConfig) handlerRefresh(w http.ResponseWriter, r *http.Request) {
	refresh_token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Refresh token not found: %s", err))
		return
	}
	dbRefreshToken, err := cfg.queries.GetRefreshToken(r.Context(), refresh_token)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("invalid refresh token: %s", err))
		return
	}
	if dbRefreshToken.RevokedAt.Valid || dbRefreshToken.ExpiresAt.Before(time.Now()) {
		respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("expired/revoked refresh token: %s", err))
		return
	}
	usr, err := cfg.queries.GetUserFromRefreshToken(r.Context(), refresh_token)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("invalid user: %s", err))
		return
	}
	token, err := auth.MakeJWT(usr.ID, cfg.secret, time.Hour)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("failed to create JWT: %s", err))
		return
	}
	respondWithJSON(w, http.StatusOK, struct {
		Token string `json:"token"`
	}{token})
}

func (cfg *apiConfig) handlerRevoke(w http.ResponseWriter, r *http.Request) {
	refresh_token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Refresh token not found: %s", err))
		return
	}
	_, err = cfg.queries.RevokeToken(r.Context(), refresh_token)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("error revoking refresh token: %s", err))
		return
	}
	respondWithJSON(w, http.StatusNoContent, struct{}{})
}

func (cfg *apiConfig) handlerUsers(w http.ResponseWriter, r *http.Request) {
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("Access token not found: %s", err))
		return
	}
	userid, err := auth.ValidateJWT(token, cfg.secret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("Invalid token: %s", err))
		return
	}
	reqBody := struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	}{}
	err = json.NewDecoder(r.Body).Decode(&reqBody)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Couldn't decode parameters: %s", err))
		return
	}
	hashed_password, err := auth.HashPassword(reqBody.Password)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Couldn't hash password: %s", err))
		return
	}
	usr, err := cfg.queries.UpdateUser(r.Context(), database.UpdateUserParams{
		Email:          reqBody.Email,
		HashedPassword: hashed_password,
		ID:             userid,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Couldn't update user: %s", err))
		return
	}
	respondWithJSON(w, http.StatusOK, User{
		ID:          usr.ID,
		CreatedAt:   usr.CreatedAt,
		UpdatedAt:   usr.UpdatedAt,
		Email:       usr.Email,
		IsChirpyRed: usr.IsChirpyRed,
	})
}

func (cfg *apiConfig) handlerDeleteChirp(w http.ResponseWriter, r *http.Request) {
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("Access token not found: %s", err))
		return
	}
	userid, err := auth.ValidateJWT(token, cfg.secret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("Invalid token: %s", err))
		return
	}
	chirpID := r.PathValue("chirpID")
	chirp_id, err := uuid.Parse(chirpID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Bad chirp UUID: %v", err))
		return
	}
	chirp, err := cfg.queries.GetChirpByID(r.Context(), chirp_id)
	if err != nil {
		respondWithError(w, http.StatusNotFound, fmt.Sprintf("Couldn't find chirp: %s", err))
		return
	}
	if chirp.UserID != userid {
		respondWithError(w, http.StatusForbidden, "Forbidden: Wrong user")
		return
	}
	err = cfg.queries.DeleteChirp(r.Context(), chirp_id)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Couldn't delete chirp: %s", err))
		return
	}
	respondWithJSON(w, http.StatusNoContent, struct{}{})
}

func (cfg *apiConfig) handlerUpgradeUser(w http.ResponseWriter, r *http.Request) {
	apiKey, err := auth.GetAPIKey(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("Couldn't find polka key: %s", err))
		return
	}
	if apiKey != cfg.polka_key {
		respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("Wrong polka key: %s", err))
		return
	}
	reqBody := struct {
		Event string `json:"event"`
		Data  struct {
			UserID string `json:"user_id"`
		} `json:"data"`
	}{}
	err = json.NewDecoder(r.Body).Decode(&reqBody)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Couldn't decode parameters: %s", err))
		return
	}
	if reqBody.Event != "user.upgraded" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	uid, err := uuid.Parse(reqBody.Data.UserID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Couldn't decode user id: %s", err))
		return
	}
	_, err = cfg.queries.UpgradeUser(r.Context(), uid)
	if err != nil {
		respondWithError(w, http.StatusNotFound, fmt.Sprintf("Couldn't find user: %s", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
