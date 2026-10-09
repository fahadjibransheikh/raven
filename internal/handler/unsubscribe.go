package handler

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	mailmessage "github.com/cristianadrielbraun/gofer/internal/mail/message"
	"github.com/cristianadrielbraun/gofer/internal/views"
)

const (
	unsubscribeTimeout      = 10 * time.Second
	unsubscribeMaxRedirects = 3
	unsubscribeMaxBody      = 64 << 10
)

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// unsubscribeAddrBlocked reports addresses a one-click unsubscribe must never reach:
// loopback, private (RFC1918, fc00::/7), link-local, unspecified, multicast, 0.0.0.0/8
// and CGNAT. The server may run headless on a remote host, so this is an SSRF guard.
func unsubscribeAddrBlocked(addr netip.Addr) bool {
	addr = addr.Unmap()
	return !addr.IsValid() || addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() ||
		cgnatPrefix.Contains(addr) || (addr.Is4() && addr.As4()[0] == 0)
}

// newUnsubscribeClient builds the HTTP client for RFC 8058 one-click requests. The address
// check runs in the dialer's Control hook, i.e. on the already-resolved IP of every
// connection (redirects included), so DNS rebinding cannot slip past a pre-flight lookup.
// No proxy, no cookie jar. blocked is a parameter only so tests can allow loopback.
func newUnsubscribeClient(blocked func(netip.Addr) bool) *http.Client {
	dialer := &net.Dialer{
		Timeout: unsubscribeTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			addr, err := netip.ParseAddr(host)
			if err != nil || blocked(addr) {
				return fmt.Errorf("unsubscribe: refusing to connect to %s", host)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: unsubscribeTimeout,
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         dialer.DialContext,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout: unsubscribeTimeout,
			DisableKeepAlives:   true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > unsubscribeMaxRedirects {
				return errors.New("unsubscribe: too many redirects")
			}
			if req.URL.Scheme != "https" {
				return errors.New("unsubscribe: redirect to a non-https URL")
			}
			return nil
		},
	}
}

var productionUnsubscribeClient = sync.OnceValue(func() *http.Client { return newUnsubscribeClient(unsubscribeAddrBlocked) })

// unsubscribeOneClick POSTs List-Unsubscribe=One-Click to an https URI. 2xx is success.
func unsubscribeOneClick(ctx context.Context, client *http.Client, rawURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil {
		return err
	}
	if req.URL.Scheme != "https" || req.URL.Hostname() == "" {
		return errors.New("unsubscribe: only https links are supported")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Raven")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, unsubscribeMaxBody))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("the sender answered %s", resp.Status)
	}
	return nil
}

// handleUnsubscribeInfo tells the reader whether (and how) this message can be unsubscribed,
// for messages whose headers were only stored once the body was fetched.
func (h *Handler) handleUnsubscribeInfo(w http.ResponseWriter, r *http.Request) {
	if _, _, err := h.getMessageInfo(r.Context(), r.PathValue("id")); err != nil {
		writeMessageTargetError(w, r, err)
		return
	}
	email, err := h.db.GetEmailByIDForUser(r.Context(), r.PathValue("id"), h.userID(r.Context()))
	if err != nil || email == nil {
		http.NotFound(w, r)
		return
	}
	method, target := views.UnsubscribeAction(email)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"method": method, "target": target})
}

// handleUnsubscribeMessage acts on the message's List-Unsubscribe headers: one-click POST,
// else a mailto email through the normal send queue, else it tells the client to open the
// sender's page in the user's browser.
func (h *Handler) handleUnsubscribeMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := r.PathValue("id")
	_, info, err := h.getMessageInfo(ctx, idStr)
	if err != nil {
		writeMessageTargetError(w, r, err)
		return
	}
	email, err := h.db.GetEmailByIDForUser(ctx, idStr, h.userID(ctx))
	if err != nil || email == nil {
		http.NotFound(w, r)
		return
	}
	u := mailmessage.ParseListUnsubscribe(email.ListUnsubscribe, email.ListUnsubscribePost)

	reply := map[string]string{}
	switch method := u.Method(); method {
	case mailmessage.UnsubscribeOneClick:
		client := h.unsubscribeClient
		if client == nil {
			client = productionUnsubscribeClient()
		}
		if err := unsubscribeOneClick(ctx, client, u.HTTPS); err != nil {
			log.Printf("unsubscribe message=%s: %v", idStr, err)
			http.Error(w, "Could not unsubscribe: "+err.Error(), http.StatusBadGateway)
			return
		}
		reply["action"] = "done"
	case mailmessage.UnsubscribeMailto:
		// Send from the address the list actually has: the identity this message was addressed to.
		identities, _ := h.db.ListAccountIdentities(ctx, h.userID(ctx), info.AccountID)
		from := pickReplyIdentity(identities, email.FolderRole, email.From, email.To, email.CC)
		if err := h.sendUnsubscribeEmail(ctx, info.AccountID, from, u.Mailto); err != nil {
			log.Printf("unsubscribe message=%s: %v", idStr, err)
			http.Error(w, "Could not send the unsubscribe email", http.StatusBadGateway)
			return
		}
		reply["action"] = "done"
	case mailmessage.UnsubscribeBrowser:
		reply["action"] = "open"
		reply["url"] = u.HTTPS
	default:
		http.Error(w, "this message has no unsubscribe link", http.StatusConflict)
		return
	}
	reply["method"] = u.Method()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}

// fromEmail "" means the account's default identity.
func (h *Handler) sendUnsubscribeEmail(ctx context.Context, accountID, fromEmail, mailto string) error {
	to, subject, body, ok := mailmessage.MailtoRequest(mailto)
	if !ok {
		return errors.New("invalid mailto address")
	}
	account, err := h.ownedAccount(ctx, accountID)
	if err != nil || account == nil {
		return errors.New("account not found")
	}
	fromName, fromEmail, cerr := h.resolveComposeIdentity(ctx, account, fromEmail)
	if cerr != nil {
		return errors.New(cerr.message)
	}
	if strings.TrimSpace(body) == "" {
		body = subject
	}
	msg := &mailmessage.OutgoingMessage{
		FromName: fromName, FromEmail: fromEmail, To: []*mail.Address{to},
		Subject: subject, TextBody: body,
		MessageID: mailmessage.NewMessageID(), Date: time.Now().UTC(),
	}
	if _, err := h.queueOutgoingMessageWithID(ctx, uuid.NewString(), accountID, 0, "", msg, time.Now().UTC(), false); err != nil {
		return err
	}
	h.signalOutgoingWorker()
	return nil
}
