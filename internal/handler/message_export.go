package handler

import (
	"bytes"
	"html"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cristianadrielbraun/gofer/internal/mail/message"
	"github.com/cristianadrielbraun/gofer/internal/views"
)

// emlFilename turns a subject into a safe download name: no path separators, quotes or control
// characters, at most 80 runes before ".eml", "message.eml" when nothing is left.
func emlFilename(subject string) string {
	var b strings.Builder
	for _, r := range subject {
		switch {
		case r == '/' || r == '\\' || r == '"' || r == ':' || r == '*' || r == '?' || r == '<' || r == '>' || r == '|' || r == '%':
			b.WriteRune(' ')
		case unicode.IsControl(r) || r == utf8.RuneError || unicode.Is(unicode.Cf, r) || unicode.IsSpace(r):
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	name := strings.Join(strings.Fields(b.String()), " ")
	if r := []rune(name); len(r) > 80 {
		name = strings.TrimSpace(string(r[:80]))
	}
	name = strings.TrimLeft(name, ". ") // no hidden or ".." names
	if name == "" {
		return "message.eml"
	}
	return name + ".eml"
}

// handleMessageRaw serves the stored RFC 822 source of an owned message as a download. The path
// comes from the database only, and must resolve inside the blob directory.
func (h *Handler) handleMessageRaw(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	msgID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || msgID <= 0 {
		http.NotFound(w, r)
		return
	}
	userID := h.userID(ctx)
	info, err := h.db.GetMessageStorageInfoForUser(ctx, msgID, userID)
	if err != nil || info == nil {
		http.NotFound(w, r)
		return
	}
	if !h.rawFileUsable(info.RawPath) {
		// Not fetched yet: the body fetch stores the source as a side effect.
		h.fetchAndStoreBody(ctx, msgID, info.AccountID)
		if info, err = h.db.GetMessageStorageInfoForUser(ctx, msgID, userID); err != nil || info == nil || !h.rawFileUsable(info.RawPath) {
			http.Error(w, "The original message is not available yet. Open the message and try again.", http.StatusNotFound)
			return
		}
	}
	f, err := os.Open(info.RawPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	var subject string
	if email, err := h.db.GetEmailByIDForFolderForUser(ctx, strconv.FormatInt(msgID, 10), "", userID); err == nil && email != nil {
		subject = email.Subject
	}
	hd := w.Header()
	hd.Set("Content-Type", "message/rfc822")
	hd.Set("Content-Disposition", `attachment; filename="`+emlFilename(subject)+`"`)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", time.Time{}, f)
}

func (h *Handler) rawFileUsable(path string) bool {
	if path == "" || h.blobStore == nil || !h.blobStore.Contains(path) {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Size() > 0
}

const printPageStyle = `<style>
html,body{background:#fff;color:#111}
body{margin:24px auto;max-width:860px;padding:0 16px;font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
.print-header{border-bottom:1px solid #bbb;margin-bottom:20px;padding-bottom:12px}
.print-header h1{font-size:20px;margin:0 0 10px}
.print-header dl{display:grid;grid-template-columns:auto 1fr;gap:2px 12px;margin:0;font-size:13px}
.print-header dt{color:#555}.print-header dd{margin:0;overflow-wrap:anywhere}
img{max-width:100%;height:auto}
@media print{body{margin:0;max-width:none;padding:0}}
</style>`

// handlePrintMessage renders a standalone, print-ready page for an owned message: a header block
// followed by the same sanitized body the reader shows. The CSP lets only our one nonce'd script
// (window.print) run, so scripts in the mail stay inert even if the sanitizer misses one.
func (h *Handler) handlePrintMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	msgID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || msgID <= 0 {
		http.NotFound(w, r)
		return
	}
	userID := h.userID(ctx)
	ctx = h.contextWithUserTimezone(ctx, r, userID)
	email, err := h.db.GetEmailByIDForFolderForUser(ctx, id, "", userID)
	if err != nil || email == nil {
		http.NotFound(w, r)
		return
	}
	body, ok := h.emailBodyHTML(ctx, id, msgID, userID, false)
	if !ok {
		http.NotFound(w, r)
		return
	}
	loadRemote := h.remoteImagesAllowed(ctx, userID, msgID)
	if loadRemote {
		body = message.RestoreRemoteImages(body)
	}
	body = h.withImageGrants(ctx, msgID, body)

	var head bytes.Buffer
	if err := views.MessagePrintHeader(email).Render(ctx, &head); err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	nonce := newCSPNonce()
	img := "img-src 'self' data:"
	if loadRemote {
		img += " https: http:"
	}
	hd := w.Header()
	// Add, not Set: keep the global frame-ancestors policy.
	hd.Add("Content-Security-Policy", "default-src 'none'; "+img+"; style-src 'unsafe-inline'; font-src data:; script-src 'nonce-"+nonce+"'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Cache-Control", "no-store")

	var doc bytes.Buffer
	doc.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>`)
	doc.WriteString(html.EscapeString(email.Subject))
	doc.WriteString(`</title>` + printPageStyle + `</head><body>`)
	doc.Write(head.Bytes())
	doc.Write(body)
	doc.WriteString(`<script nonce="` + nonce + `">window.addEventListener("load",function(){window.print()})</script></body></html>`)
	w.Write(doc.Bytes())
}
