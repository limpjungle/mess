package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"messenger/internal/protocol"
	"messenger/internal/store"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 30 * time.Second
)

type conn struct {
	ws        *websocket.Conn
	sessionID string
	userID    int64
	username  string
	send      chan []byte
}

type Hub struct {
	store store.Store
	crypt *Cryptor

	mu       sync.RWMutex
	sessions map[string]*conn
	users    map[string]*conn
}

func NewHub(s store.Store, c *Cryptor) *Hub {
	return &Hub{
		store:    s,
		crypt:    c,
		sessions: make(map[string]*conn),
		users:    make(map[string]*conn),
	}
}

func (h *Hub) handleWS(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		http.Error(w, "missing session", http.StatusUnauthorized)
		return
	}
	userID, username, ok := h.validSession(r.Context(), sessionID)
	if !ok {
		http.Error(w, "invalid session", http.StatusUnauthorized)
		return
	}

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade: %v", err)
		return
	}

	c := &conn{
		ws:        ws,
		sessionID: sessionID,
		userID:    userID,
		username:  username,
		send:      make(chan []byte, 256),
	}

	h.mu.Lock()
	h.sessions[sessionID] = c
	h.users[username] = c
	h.mu.Unlock()

	go h.writePump(c)
	go h.readPump(c)

	h.deliverPending(c)
}

func (h *Hub) validSession(ctx context.Context, id string) (int64, string, bool) {
	sess, err := h.store.GetSession(ctx, id)
	if err != nil {
		return 0, "", false
	}
	if time.Now().Unix() > sess.ExpiresAt {
		h.store.DeleteSession(ctx, id)
		return 0, "", false
	}
	return sess.UserID, sess.Username, true
}

func (h *Hub) deliverPending(c *conn) {
	pending, err := h.store.PendingMessages(context.Background(), c.username)
	if err != nil {
		log.Printf("pending: %v", err)
		return
	}
	ids := make([]int64, 0, len(pending))
	msgs := make([]protocol.Message, 0, len(pending))
	for _, m := range pending {
		text, err := h.crypt.Decrypt(m.Ciphertext, m.Nonce)
		if err != nil {
			log.Printf("decrypt pending %d: %v", m.ID, err)
			continue
		}
		msgs = append(msgs, protocol.Message{
			ID: m.ID, From: m.Recipient, To: c.username, Text: string(text), CreatedAt: m.CreatedAt,
		})
		ids = append(ids, m.ID)
	}
	if len(msgs) > 0 {
		payload, _ := json.Marshal(protocol.Envelope{Type: protocol.TypePending, Data: protocol.PendingMsg{Messages: msgs}})
		c.send <- payload
		if err := h.store.DeleteDelivered(context.Background(), ids); err != nil {
			log.Printf("delete delivered: %v", err)
		}
	}
}

func (h *Hub) readPump(c *conn) {
	defer func() {
		h.unregister(c)
		c.ws.Close()
		// session ended: drop undelivered messages addressed to this user
		h.store.DeleteRecipientMessages(context.Background(), c.username)
	}()

	c.ws.SetReadLimit(1 << 20)
	c.ws.SetReadDeadline(time.Now().Add(pongWait))
	c.ws.SetPongHandler(func(string) error {
		c.ws.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			h.sendError(c, "bad message")
			continue
		}
		switch env.Type {
		case protocol.TypeSend:
			h.handleSend(c, env.Data)
		}
	}
}

func (h *Hub) handleSend(c *conn, data any) {
	raw, _ := json.Marshal(data)
	var msg protocol.SendMsg
	if err := json.Unmarshal(raw, &msg); err != nil || msg.To == "" || msg.Text == "" {
		h.sendError(c, "invalid send payload")
		return
	}

	h.mu.RLock()
	recip, online := h.users[msg.To]
	h.mu.RUnlock()

	if online {
		out := protocol.Message{From: c.username, To: msg.To, Text: msg.Text, CreatedAt: time.Now().Unix()}
		payload, _ := json.Marshal(protocol.Envelope{Type: protocol.TypeMessage, Data: out})
		recip.send <- payload
		return
	}

	if !h.userExists(msg.To) {
		h.sendError(c, "no such user: "+msg.To)
		return
	}

	ct, nonce, err := h.crypt.Encrypt([]byte(msg.Text))
	if err != nil {
		h.sendError(c, "encrypt failed")
		return
	}
	if _, err := h.store.SaveMessage(context.Background(), c.userID, msg.To, ct, nonce); err != nil {
		log.Printf("save message: %v", err)
		h.sendError(c, "storage failed")
	}
}

func (h *Hub) userExists(username string) bool {
	users, err := h.store.ListUsers(context.Background())
	if err != nil {
		log.Printf("list users: %v", err)
		return false
	}
	for _, u := range users {
		if u.Username == username {
			return true
		}
	}
	return false
}

func (h *Hub) sendError(c *conn, msg string) {
	payload, _ := json.Marshal(protocol.Envelope{Type: protocol.TypeError, Data: protocol.ErrorMsg{Message: msg}})
	c.send <- payload
}

func (h *Hub) writePump(c *conn) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.ws.Close()
	}()
	for {
		select {
		case payload, ok := <-c.send:
			c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.ws.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.ws.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (h *Hub) unregister(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[c.sessionID] == c {
		delete(h.sessions, c.sessionID)
	}
	if h.users[c.username] == c {
		delete(h.users, c.username)
	}
	close(c.send)
}

func (h *Hub) unregisterSession(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.sessions[sessionID]; ok {
		if h.users[c.username] == c {
			delete(h.users, c.username)
		}
		delete(h.sessions, sessionID)
		close(c.send)
	}
}
