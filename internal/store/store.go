package store

import (
	"context"
	"errors"
)

var ErrSessionNotFound = errors.New("session not found")

type User struct {
	ID       int64
	Username string
}

type Session struct {
	ID        string
	UserID    int64
	Username  string
	ExpiresAt int64
}

type Message struct {
	ID         int64
	SenderID   int64
	Recipient  string
	Ciphertext []byte
	Nonce      []byte
	CreatedAt  int64
}

type Store interface {
	CreateUser(ctx context.Context, username, passwordHash string) (User, error)
	GetUserByUsername(ctx context.Context, username string) (User, string, error)
	ListUsers(ctx context.Context) ([]User, error)

	CreateSession(ctx context.Context, id string, userID int64, expiresAt int64) error
	GetSession(ctx context.Context, id string) (Session, error)
	DeleteSession(ctx context.Context, id string) error

	SaveMessage(ctx context.Context, senderID int64, recipient string, ciphertext, nonce []byte) (int64, error)
	PendingMessages(ctx context.Context, username string) ([]Message, error)
	DeleteDelivered(ctx context.Context, ids []int64) error
	// DeleteRecipientMessages removes undelivered messages addressed to a user.
	// Called when the user's session ends (disconnect/logout/expiry).
	DeleteRecipientMessages(ctx context.Context, username string) error
	// CleanupExpired deletes expired sessions and undelivered messages
	// addressed to users with no active session. Returns deleted sessions count.
	CleanupExpired(ctx context.Context) (int64, error)
}
