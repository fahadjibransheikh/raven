package models

import "time"

const (
	MailSecurityExceptionHTTPDiscovery      = "http_discovery"
	MailSecurityExceptionPlaintextTransport = "plaintext_transport"
	MailSecurityExceptionPrivateTarget      = "private_target"
)

type MailSecurityException struct {
	ID        string
	Kind      string
	Protocol  string
	Host      string
	Port      int
	CreatedBy string
	CreatedAt time.Time
	Accounts  []MailSecurityExceptionAccount
}

type MailSecurityExceptionAccount struct {
	ID    string
	Email string
}

type MailSecurityAdminData struct {
	// ActionBase is where the panel's forms post: the admin page in managed
	// and open mode, the owner's security settings in personal mode.
	ActionBase     string
	Exceptions     []MailSecurityException
	Notice         string
	Error          string
	StepUpRequired bool
	CSRFTokens     map[string]string
}
