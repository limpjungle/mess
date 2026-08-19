package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrUsernameTaken = errors.New("username already taken")

type PG struct {
	pool *pgxpool.Pool
}

func Connect(ctx context.Context, dsn string) (*PG, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &PG{pool: pool}, nil
}

func (p *PG) Close() { p.pool.Close() }

func (p *PG) Migrate(ctx context.Context) error {
	_, err := p.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    username      TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE TABLE IF NOT EXISTS messages (
    id          BIGSERIAL PRIMARY KEY,
    sender_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient   TEXT NOT NULL,
    ciphertext  BYTEA NOT NULL,
    nonce       BYTEA NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_messages_recipient ON messages(recipient);
`)
	return err
}

func (p *PG) CreateUser(ctx context.Context, username, passwordHash string) (User, error) {
	var u User
	err := p.pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id, username`,
		username, passwordHash).Scan(&u.ID, &u.Username)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return User{}, ErrUsernameTaken
		}
		return User{}, err
	}
	return u, nil
}

func (p *PG) GetUserByUsername(ctx context.Context, username string) (User, string, error) {
	var u User
	var hash string
	err := p.pool.QueryRow(ctx,
		`SELECT id, username, password_hash FROM users WHERE username = $1`,
		username).Scan(&u.ID, &u.Username, &hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, "", ErrUsernameTaken
		}
		return User{}, "", err
	}
	return u, hash, nil
}

func (p *PG) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, username FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (p *PG) CreateSession(ctx context.Context, id string, userID int64, expiresAt int64) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, expires_at) VALUES ($1, $2, to_timestamp($3))`,
		id, userID, expiresAt)
	return err
}

func (p *PG) GetSession(ctx context.Context, id string) (Session, error) {
	var s Session
	err := p.pool.QueryRow(ctx,
		`SELECT s.id, s.user_id, u.username, extract(epoch from s.expires_at)::bigint
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.id = $1`, id).Scan(&s.ID, &s.UserID, &s.Username, &s.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrSessionNotFound
		}
		return Session{}, err
	}
	return s, nil
}

func (p *PG) DeleteSession(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	return err
}

func (p *PG) SaveMessage(ctx context.Context, senderID int64, recipient string, ciphertext, nonce []byte) (int64, error) {
	var id int64
	err := p.pool.QueryRow(ctx,
		`INSERT INTO messages (sender_id, recipient, ciphertext, nonce)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		senderID, recipient, ciphertext, nonce).Scan(&id)
	return id, err
}

func (p *PG) PendingMessages(ctx context.Context, username string) ([]Message, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT m.id, m.sender_id, u.username, m.ciphertext, m.nonce,
		        extract(epoch from m.created_at)::bigint
		 FROM messages m JOIN users u ON u.id = m.sender_id
		 WHERE m.recipient = $1 ORDER BY m.created_at`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SenderID, &m.Recipient, &m.Ciphertext, &m.Nonce, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (p *PG) DeleteDelivered(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := p.pool.Exec(ctx, `DELETE FROM messages WHERE id = ANY($1)`, ids)
	return err
}

func (p *PG) DeleteRecipientMessages(ctx context.Context, username string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM messages WHERE recipient = $1`, username)
	return err
}

func (p *PG) CleanupExpired(ctx context.Context) (int64, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var deleted int64
	if err := tx.QueryRow(ctx,
		`DELETE FROM sessions WHERE expires_at < now() RETURNING count(*)`).Scan(&deleted); err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx,
		`DELETE FROM messages WHERE recipient NOT IN (
		   SELECT u.username FROM users u
		   JOIN sessions s ON s.user_id = u.id
		   WHERE s.expires_at > now()
		 )`)
	if err != nil {
		return 0, err
	}
	return deleted, tx.Commit(ctx)
}
