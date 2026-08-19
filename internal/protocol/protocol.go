package protocol

const (
	TypeSend     = "send"
	TypeMessage  = "message"
	TypePending  = "pending"
	TypeError    = "error"
	TypeAck      = "ack"
	TypePing     = "ping"
	TypePresence = "presence"
)

type Envelope struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

type SendMsg struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

type Message struct {
	ID        int64  `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Text      string `json:"text"`
	CreatedAt int64  `json:"created_at"`
}

type PendingMsg struct {
	Messages []Message `json:"messages"`
}

type ErrorMsg struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type AckMsg struct {
	ID int64 `json:"id,omitempty"`
}
