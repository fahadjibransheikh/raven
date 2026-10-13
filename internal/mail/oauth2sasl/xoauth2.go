package oauth2sasl

import (
	"errors"
	"strings"

	"github.com/emersion/go-sasl"
)

const mechanism = "XOAUTH2"

type client struct {
	username string
	token    string
}

func NewClient(username, token string) sasl.Client {
	return &client{username: username, token: token}
}

// ErrInvalidCredentials reports a username or token containing the \x01
// separator, which would let it add fields to the login message.
var ErrInvalidCredentials = errors.New("XOAUTH2 username or token contains a control character")

func (c *client) Start() (string, []byte, error) {
	if strings.ContainsRune(c.username, '\x01') || strings.ContainsRune(c.token, '\x01') {
		return "", nil, ErrInvalidCredentials
	}
	return mechanism, []byte("user=" + c.username + "\x01auth=Bearer " + c.token + "\x01\x01"), nil
}

func (c *client) Next(challenge []byte) ([]byte, error) {
	return nil, sasl.ErrUnexpectedServerChallenge
}
