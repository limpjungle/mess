package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"messenger/internal/client"
)

func main() {
	addr := flag.String("addr", "https://localhost:8443", "server address")
	flag.Parse()

	c := client.New(*addr)
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("messenger CLI. commands: register, login, users, chat, logout, exit")
	fmt.Print("> ")

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err.Error() == "EOF" {
				return
			}
			fmt.Printf("read: %v\n", err)
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			fmt.Print("> ")
			continue
		}
		parts := strings.Fields(line)
		cmd := strings.ToLower(parts[0])
		args := parts[1:]

		switch cmd {
		case "exit", "quit":
			if c.LoggedIn() {
				c.Logout()
			}
			return
		case "register":
			if len(args) != 1 {
				fmt.Println("usage: register <username>")
			} else if err := c.Register(args[0], readPassword(reader)); err != nil {
				fmt.Printf("register: %v\n", err)
			} else {
				fmt.Println("registered")
			}
		case "login":
			if len(args) != 1 {
				fmt.Println("usage: login <username>")
			} else if err := c.Login(args[0], readPassword(reader)); err != nil {
				fmt.Printf("login: %v\n", err)
			} else {
				fmt.Printf("logged in as %s\n", c.Username())
			}
		case "users":
			users, err := c.Users()
			if err != nil {
				fmt.Printf("users: %v\n", err)
			} else if len(users) == 0 {
				fmt.Println("no users")
			} else {
				fmt.Println(strings.Join(users, "  "))
			}
		case "chat":
			if !c.LoggedIn() {
				fmt.Println("login first")
			} else if len(args) != 1 {
				fmt.Println("usage: chat <username>")
			} else if ch, err := c.OpenChat(args[0]); err != nil {
				fmt.Printf("chat: %v\n", err)
			} else if err := ch.Run(reader); err != nil {
				fmt.Printf("chat ended: %v\n", err)
			}
		case "logout":
			if err := c.Logout(); err != nil {
				fmt.Printf("logout: %v\n", err)
			} else {
				fmt.Println("logged out")
			}
		default:
			fmt.Println("unknown command:", cmd)
		}
		fmt.Print("> ")
	}
}

func readPassword(reader *bufio.Reader) string {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Print("password: ")
		pw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return ""
		}
		return string(pw)
	}
	pw, _ := reader.ReadString('\n')
	return strings.TrimSpace(pw)
}
