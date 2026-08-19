package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

const (
	sessionTTL    = 24 * time.Hour
	sessionIDSize = 32
)

func newSessionID() (string, error) {
	b := make([]byte, sessionIDSize)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Server) checkSession(ctx context.Context, id string) (userID int64, username string, ok bool) {
	sess, err := s.store.GetSession(ctx, id)
	if err != nil {
		return 0, "", false
	}
	if time.Now().Unix() > sess.ExpiresAt {
		s.store.DeleteSession(ctx, id)
		return 0, "", false
	}
	return sess.UserID, sess.Username, true
}
