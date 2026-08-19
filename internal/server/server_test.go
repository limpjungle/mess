package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"

	"messenger/internal/protocol"
	"messenger/internal/store"
)

type fakeStore struct {
	mu       sync.Mutex
	nextID   int64
	users    map[string]fakeUser // username -> user
	sessions map[string]store.Session
	messages []store.Message
}

type fakeUser struct {
	user store.User
	hash string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users:    map[string]fakeUser{},
		sessions: map[string]store.Session{},
	}
}

func (f *fakeStore) CreateUser(_ context.Context, username, hash string) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[username]; ok {
		return store.User{}, store.ErrUsernameTaken
	}
	f.nextID++
	u := store.User{ID: f.nextID, Username: username}
	f.users[username] = fakeUser{user: u, hash: hash}
	return u, nil
}

func (f *fakeStore) GetUserByUsername(_ context.Context, username string) (store.User, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fu, ok := f.users[username]
	if !ok {
		return store.User{}, "", store.ErrUsernameTaken
	}
	return fu.user, fu.hash, nil
}

func (f *fakeStore) ListUsers(_ context.Context) ([]store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.User, 0, len(f.users))
	for _, fu := range f.users {
		out = append(out, fu.user)
	}
	return out, nil
}

func (f *fakeStore) CreateSession(_ context.Context, id string, userID int64, expiresAt int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	username := ""
	for _, fu := range f.users {
		if fu.user.ID == userID {
			username = fu.user.Username
		}
	}
	f.sessions[id] = store.Session{ID: id, UserID: userID, Username: username, ExpiresAt: expiresAt}
	return nil
}

func (f *fakeStore) GetSession(_ context.Context, id string) (store.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return store.Session{}, store.ErrSessionNotFound
	}
	return s, nil
}

func (f *fakeStore) DeleteSession(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, id)
	return nil
}

func (f *fakeStore) SaveMessage(_ context.Context, senderID int64, recipient string, ct, nonce []byte) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := int64(len(f.messages) + 1)
	f.messages = append(f.messages, store.Message{ID: id, SenderID: senderID, Recipient: recipient, Ciphertext: ct, Nonce: nonce})
	return id, nil
}

func (f *fakeStore) PendingMessages(_ context.Context, username string) ([]store.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.Message
	for _, m := range f.messages {
		if m.Recipient == username {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteDelivered(_ context.Context, ids []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	drop := map[int64]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	kept := f.messages[:0]
	for _, m := range f.messages {
		if !drop[m.ID] {
			kept = append(kept, m)
		}
	}
	f.messages = kept
	return nil
}

func (f *fakeStore) DeleteRecipientMessages(_ context.Context, username string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.messages[:0]
	for _, m := range f.messages {
		if m.Recipient != username {
			kept = append(kept, m)
		}
	}
	f.messages = kept
	return nil
}

func (f *fakeStore) CleanupExpired(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().Unix()
	var deleted int64
	for id, s := range f.sessions {
		if s.ExpiresAt < now {
			delete(f.sessions, id)
			deleted++
		}
	}
	active := map[string]bool{}
	for _, s := range f.sessions {
		username := ""
		for _, fu := range f.users {
			if fu.user.ID == s.UserID {
				username = fu.user.Username
			}
		}
		active[username] = true
	}
	kept := f.messages[:0]
	for _, m := range f.messages {
		if active[m.Recipient] {
			kept = append(kept, m)
		}
	}
	f.messages = kept
	return deleted, nil
}

func (f *fakeStore) messageCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.messages)
}

func startTestServer(t *testing.T) (*httptest.Server, *fakeStore) {
	t.Helper()
	fs := newFakeStore()
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	fs.CreateUser(context.Background(), "alice", string(hash))
	fs.CreateUser(context.Background(), "bob", string(hash))

	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	crypt, err := NewCryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(New(fs, crypt).Handler())
	t.Cleanup(srv.Close)
	return srv, fs
}

func login(t *testing.T, srv *httptest.Server, username string) string {
	t.Helper()
	body := strings.NewReader(`{"username":"` + username + `","password":"secret"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/login", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.SessionID == "" {
		t.Fatalf("login failed for %s", username)
	}
	return out.SessionID
}

func dialWS(t *testing.T, srv *httptest.Server, session string) *websocket.Conn {
	t.Helper()
	url := strings.Replace(srv.URL, "https", "wss", 1) + "/ws?session=" + session
	tlsCfg := srv.Client().Transport.(*http.Transport).TLSClientConfig
	dialer := websocket.Dialer{TLSClientConfig: tlsCfg}
	ws, _, err := dialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

func readMsg(t *testing.T, ws *websocket.Conn) protocol.Message {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	var m protocol.Message
	b, _ := json.Marshal(env.Data)
	json.Unmarshal(b, &m)
	return m
}

func readPending(t *testing.T, ws *websocket.Conn) []protocol.Message {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != protocol.TypePending {
		t.Fatalf("expected pending, got %s", env.Type)
	}
	b, _ := json.Marshal(env.Data)
	var p protocol.PendingMsg
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p.Messages
}

func TestOnlineDelivery(t *testing.T) {
	srv, _ := startTestServer(t)
	bob := dialWS(t, srv, login(t, srv, "bob"))
	alice := dialWS(t, srv, login(t, srv, "alice"))

	alice.WriteJSON(protocol.Envelope{Type: protocol.TypeSend, Data: protocol.SendMsg{To: "bob", Text: "privet"}})

	got := readMsg(t, bob)
	if got.From != "alice" || got.Text != "privet" {
		t.Fatalf("got %+v", got)
	}
}

func TestOfflinePendingAndCleanup(t *testing.T) {
	srv, fs := startTestServer(t)

	// alice offline: send goes to DB
	alice := dialWS(t, srv, login(t, srv, "alice"))
	alice.WriteJSON(protocol.Envelope{Type: protocol.TypeSend, Data: protocol.SendMsg{To: "bob", Text: "secret1"}})
	alice.WriteJSON(protocol.Envelope{Type: protocol.TypeSend, Data: protocol.SendMsg{To: "bob", Text: "secret2"}})
	time.Sleep(100 * time.Millisecond)
	if fs.messageCount() != 2 {
		t.Fatalf("expected 2 stored, got %d", fs.messageCount())
	}

	// bob connects: receives pending (one envelope, both messages), then deleted
	bob := dialWS(t, srv, login(t, srv, "bob"))
	got := readPending(t, bob)
	texts := ""
	for _, m := range got {
		texts += m.Text + " "
	}
	if !strings.Contains(texts, "secret1") || !strings.Contains(texts, "secret2") {
		t.Fatalf("unexpected texts: %q", texts)
	}
	time.Sleep(100 * time.Millisecond)
	if fs.messageCount() != 0 {
		t.Fatalf("pending not deleted: %d", fs.messageCount())
	}
}

func TestCleanupOnDisconnect(t *testing.T) {
	srv, fs := startTestServer(t)
	alice := dialWS(t, srv, login(t, srv, "alice"))
	alice.WriteJSON(protocol.Envelope{Type: protocol.TypeSend, Data: protocol.SendMsg{To: "bob", Text: "leftovers"}})
	time.Sleep(100 * time.Millisecond)
	if fs.messageCount() != 1 {
		t.Fatalf("expected 1 stored, got %d", fs.messageCount())
	}
	// sender disconnect must NOT delete the message addressed to bob
	alice.Close()
	time.Sleep(200 * time.Millisecond)
	if fs.messageCount() != 1 {
		t.Fatalf("sender disconnect deleted recipient message, got %d", fs.messageCount())
	}
	// recipient disconnect removes undelivered messages addressed to them
	bob := dialWS(t, srv, login(t, srv, "bob"))
	bob.Close()
	time.Sleep(200 * time.Millisecond)
	if fs.messageCount() != 0 {
		t.Fatalf("expected cleanup on recipient disconnect, got %d", fs.messageCount())
	}
}

func TestUnknownRecipient(t *testing.T) {
	srv, _ := startTestServer(t)
	alice := dialWS(t, srv, login(t, srv, "alice"))
	alice.WriteJSON(protocol.Envelope{Type: protocol.TypeSend, Data: protocol.SendMsg{To: "nobody", Text: "hi"}})

	alice.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := alice.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var env protocol.Envelope
	json.Unmarshal(data, &env)
	if env.Type != protocol.TypeError {
		t.Fatalf("expected error, got %s", env.Type)
	}
}
