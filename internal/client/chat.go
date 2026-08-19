package client

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"messenger/internal/protocol"
)

type Chat struct {
	client *Client
	to     string
	ws     *websocket.Conn
	mu     sync.Mutex
}

func (c *Client) OpenChat(to string) (*Chat, error) {
	host := strings.TrimPrefix(strings.TrimPrefix(c.baseURL, "https://"), "http://")
	u := url.URL{Scheme: "wss", Host: host, Path: "/ws"}
	q := u.Query()
	q.Set("session", c.sessionID)
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	ws, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return nil, err
	}
	ch := &Chat{client: c, to: to, ws: ws}
	return ch, nil
}

func (ch *Chat) Close() error { return ch.ws.Close() }

func (ch *Chat) Send(text string) error {
	payload, _ := json.Marshal(protocol.Envelope{
		Type: protocol.TypeSend,
		Data: protocol.SendMsg{To: ch.to, Text: text},
	})
	ch.mu.Lock()
	defer ch.mu.Unlock()
	return ch.ws.WriteMessage(websocket.TextMessage, payload)
}

// Run prints incoming messages until the user types "exit"/"quit"
// or the connection drops. Returns when the chat ends.
func (ch *Chat) Run(input io.Reader) error {
	defer ch.ws.Close()

	done := make(chan struct{})
	go ch.readLoop(done)

	scanner := bufio.NewScanner(input)
	fmt.Printf("chat with %s (exit — leave)\n", ch.to)
	fmt.Print("> ")
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.EqualFold(line, "exit") || strings.EqualFold(line, "quit") {
			break
		}
		if line == "" {
			fmt.Print("> ")
			continue
		}
		payload, _ := json.Marshal(protocol.Envelope{
			Type: protocol.TypeSend,
			Data: protocol.SendMsg{To: ch.to, Text: line},
		})
		ch.mu.Lock()
		err := ch.ws.WriteMessage(websocket.TextMessage, payload)
		ch.mu.Unlock()
		if err != nil {
			fmt.Printf("send: %v\n", err)
			break
		}
		fmt.Print("> ")
	}
	ch.ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	<-done
	return nil
}

func (ch *Chat) readLoop(done chan struct{}) {
	defer close(done)
	for {
		ch.ws.SetReadDeadline(time.Now().Add(3 * time.Minute))
		_, data, err := ch.ws.ReadMessage()
		if err != nil {
			return
		}
		var env protocol.Envelope
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		switch env.Type {
		case protocol.TypeMessage:
			var m protocol.Message
			json.Unmarshal(mustJSON(env.Data), &m)
			fmt.Printf("\r\033[K[%s] %s\n> ", m.From, m.Text)
		case protocol.TypePending:
			var p protocol.PendingMsg
			json.Unmarshal(mustJSON(env.Data), &p)
			for _, m := range p.Messages {
				fmt.Printf("\r\033[K[%s] %s\n> ", m.From, m.Text)
			}
		case protocol.TypeError:
			var e protocol.ErrorMsg
			json.Unmarshal(mustJSON(env.Data), &e)
			fmt.Printf("\r\033[K[error] %s\n> ", e.Message)
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
