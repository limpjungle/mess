package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"messenger/internal/store"
)

type Server struct {
	store store.Store
	crypt *Cryptor
	hub   *Hub
}

func New(s store.Store, crypt *Cryptor) *Server {
	return &Server{store: s, crypt: crypt, hub: NewHub(s, crypt)}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /register", s.handleRegister)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /users", s.handleUsers)
	mux.HandleFunc("GET /ws", s.hub.handleWS)
	return mux
}

type authReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req authReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || len(req.Password) < 4 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username required, password min 4 chars"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if _, err := s.store.CreateUser(r.Context(), req.Username, string(hash)); err != nil {
		if errors.Is(err, store.ErrUsernameTaken) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "username taken"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req authReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	user, hash, err := s.store.GetUserByUsername(r.Context(), strings.TrimSpace(req.Username))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	id, err := newSessionID()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	expiresAt := time.Now().Add(sessionTTL).Unix()
	if err := s.store.CreateSession(r.Context(), id, user.ID, expiresAt); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session_id": id, "username": user.Username})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	id := sessionFromRequest(r)
	if id == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing session"})
		return
	}
	sess, err := s.store.GetSession(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid session"})
		return
	}
	if time.Now().Unix() > sess.ExpiresAt {
		s.store.DeleteSession(r.Context(), id)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid session"})
		return
	}
	s.store.DeleteSession(r.Context(), id)
	s.hub.unregisterSession(id)
	// session ended: drop undelivered messages addressed to this user
	s.store.DeleteRecipientMessages(r.Context(), sess.Username)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	id := sessionFromRequest(r)
	if _, _, ok := s.checkSession(r.Context(), id); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid session"})
		return
	}
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	names := make([]string, 0, len(users))
	for _, u := range users {
		names = append(names, u.Username)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": names})
}

func sessionFromRequest(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}
