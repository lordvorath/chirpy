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
}

type User struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Email     string    `json:"email"`
	Password  string    `json:"-"`
}

func main() {
	godotenv.Load()
	dbURL := os.Getenv("DB_URL")
	platform := os.Getenv("PLATFORM")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal("failed to open db connection")
	}
	const filepathRoot = "."
	const port = "8080"
	apiCfg := apiConfig{
		fileserverHits: atomic.Int32{},
		queries:        database.New(db),
		platform:       platform,
	}

	mux := http.NewServeMux()
	mux.Handle("/app/", apiCfg.middlewareMetricsInc(http.StripPrefix("/app", http.FileServer(http.Dir(filepathRoot)))))
	mux.HandleFunc("GET /api/healthz", handlerReadiness)
	mux.HandleFunc("POST /api/users", apiCfg.handlerCreateUser)
	mux.HandleFunc("POST /api/login", apiCfg.handlerLogin)
	mux.HandleFunc("POST /api/chirps", apiCfg.handlerCreateChirp)
	mux.HandleFunc("GET /api/chirps", apiCfg.handlerGetChirps)
	mux.HandleFunc("GET /api/chirps/{chirpID}", apiCfg.handlerGetChirpByID)
	mux.HandleFunc("GET /admin/metrics", apiCfg.handlerMetrics)
	mux.HandleFunc("POST /admin/reset", apiCfg.handlerReset)

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
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Chirp is too long"))
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
		UserID: params.UserID,
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
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to retrieve chirp: %v", err))
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
		ID:        usr.ID,
		CreatedAt: usr.CreatedAt,
		UpdatedAt: usr.UpdatedAt,
		Email:     usr.Email,
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
	nuser := User{
		ID:        usr.ID,
		Email:     usr.Email,
		CreatedAt: usr.CreatedAt,
		UpdatedAt: usr.UpdatedAt,
	}
	respondWithJSON(w, http.StatusOK, nuser)
}
