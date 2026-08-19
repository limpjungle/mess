package client

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	baseURL   string
	sessionID string
	username  string
	http      *http.Client
}

func New(addr string) *Client {
	base := strings.TrimSuffix(addr, "/")
	return &Client{
		baseURL: base,
		http: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

func (c *Client) Username() string  { return c.username }
func (c *Client) SessionID() string { return c.sessionID }
func (c *Client) LoggedIn() bool    { return c.sessionID != "" }

func (c *Client) Register(username, password string) error {
	return c.post("/register", authReq{Username: username, Password: password}, nil)
}

func (c *Client) Login(username, password string) error {
	var resp struct {
		SessionID string `json:"session_id"`
		Username  string `json:"username"`
		Error     string `json:"error"`
	}
	if err := c.post("/login", authReq{Username: username, Password: password}, &resp); err != nil {
		return err
	}
	if resp.SessionID == "" {
		return fmt.Errorf("login failed: %s", resp.Error)
	}
	c.sessionID = resp.SessionID
	c.username = resp.Username
	return nil
}

func (c *Client) Logout() error {
	if c.sessionID == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/logout", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.sessionID)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	c.sessionID = ""
	c.username = ""
	return nil
}

func (c *Client) Users() ([]string, error) {
	var resp struct {
		Users []string `json:"users"`
		Error string   `json:"error"`
	}
	if err := c.get("/users", &resp); err != nil {
		return nil, err
	}
	return resp.Users, nil
}

type authReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (c *Client) post(path string, body, out any) error {
	raw, _ := json.Marshal(body)
	resp, err := c.http.Post(c.baseURL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decode(resp, out)
}

func (c *Client) get(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.sessionID)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decode(resp, out)
}

func decode(resp *http.Response, out any) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(body, &e)
		return fmt.Errorf("%s", e.Error)
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}
