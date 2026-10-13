package handler

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testPNG = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 64)...)

// fakeDownloader counts downloads and answers with a different image for
// each one, so a response shows which download it came from.
type fakeDownloader struct {
	calls, inflight, maxInflight atomic.Int32
	hold                         chan struct{} // when set, downloads wait for it to close
	reply                        func(url string) ([]byte, error)
}

func (f *fakeDownloader) download(_ context.Context, url string) ([]byte, error) {
	n := f.calls.Add(1)
	cur := f.inflight.Add(1)
	defer f.inflight.Add(-1)
	for {
		max := f.maxInflight.Load()
		if cur <= max || f.maxInflight.CompareAndSwap(max, cur) {
			break
		}
	}
	if f.hold != nil {
		<-f.hold
	}
	if f.reply != nil {
		return f.reply(url)
	}
	return append(append([]byte{}, testPNG...), byte(n)), nil
}

func relayGet(t *testing.T, h *Handler, link string) *httptest.ResponseRecorder {
	t.Helper()
	req, _, ok := h.verifiedMessageFileRequest(signedRequest(t, h, link))
	if !ok {
		t.Fatalf("link refused: %s", link)
	}
	rec := httptest.NewRecorder()
	h.relayRemoteContent(rec, req)
	return rec
}

func webLink(h *Handler, user string, msgID int64, remoteURL string) string {
	return h.remoteContentLinks(context.Background(), user, msgID)(remoteURL)
}

func keptFiles(t *testing.T, h *Handler, msgID int64) []string {
	t.Helper()
	entries, _ := os.ReadDir(h.blobStore.RemoteAssetsDir("victim-account", msgID))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func insertSecondMessage(t *testing.T, h *Handler) {
	t.Helper()
	db := h.db
	if _, err := db.Write().ExecContext(t.Context(), `INSERT INTO messages (id, account_id, internet_message_id, thread_id, subject, from_email)
		VALUES (102, 'victim-account', '<second@example.com>', 'second-thread', 'Second', 'sender@example.com')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().ExecContext(t.Context(), `INSERT INTO message_folder_state (message_id, folder_id, remote_uid) VALUES (102, 'victim-inbox', 43)`); err != nil {
		t.Fatal(err)
	}
}

func TestRelayServesRepeatRequestsFromTheKeptImage(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fixture := insertVictimReadableMessage(t, h, db)
	fake := &fakeDownloader{}
	h.remoteResourceDownloader = fake.download
	link := webLink(h, "owner", fixture.messageID, "https://img.example/a.png")

	first := relayGet(t, h, link)
	second := relayGet(t, h, link)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("status = %d, %d", first.Code, second.Code)
	}
	if fake.calls.Load() != 1 {
		t.Fatalf("downloaded %d times, want 1", fake.calls.Load())
	}
	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) || first.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("repeat differs or has type %q", second.Header().Get("Content-Type"))
	}
	if got := keptFiles(t, h, fixture.messageID); len(got) != 2 { // the fixture's own asset and this image
		t.Fatalf("kept files = %v", got)
	}
}

func TestRelayCollapsesConcurrentDownloads(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fixture := insertVictimReadableMessage(t, h, db)
	fake := &fakeDownloader{hold: make(chan struct{})}
	h.remoteResourceDownloader = fake.download
	link := webLink(h, "owner", fixture.messageID, "https://img.example/a.png")

	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func() { defer wg.Done(); codes[i] = relayGet(t, h, link).Code }()
	}
	deadline := time.Now().Add(5 * time.Second)
	for fake.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // let the others queue behind the first
	close(fake.hold)
	wg.Wait()
	for _, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("codes = %v", codes)
		}
	}
	if fake.calls.Load() != 1 {
		t.Fatalf("downloaded %d times for 8 requests, want 1", fake.calls.Load())
	}
}

func TestRelayKeepsEachMessagesImagesApart(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fixture := insertVictimReadableMessage(t, h, db)
	insertSecondMessage(t, h)
	fake := &fakeDownloader{}
	h.remoteResourceDownloader = fake.download
	const remote = "https://img.example/shared.png"

	own := relayGet(t, h, webLink(h, "owner", fixture.messageID, remote)).Body.Bytes()
	other := relayGet(t, h, webLink(h, "owner", 102, remote)).Body.Bytes()
	if fake.calls.Load() != 2 || bytes.Equal(own, other) {
		t.Fatalf("second message used the first one's copy: calls=%d", fake.calls.Load())
	}
	// A link signed for someone who doesn't own the message never reads, or
	// adds to, its copies.
	before := keptFiles(t, h, fixture.messageID)
	foreign := relayGet(t, h, webLink(h, "attacker", fixture.messageID, remote)).Body.Bytes()
	if bytes.Equal(foreign, own) || fake.calls.Load() != 3 {
		t.Fatalf("a foreign link was served the owner's copy: calls=%d", fake.calls.Load())
	}
	if after := keptFiles(t, h, fixture.messageID); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Fatalf("a foreign link changed the owner's files: %v -> %v", before, after)
	}
	if again := relayGet(t, h, webLink(h, "owner", fixture.messageID, remote)).Body.Bytes(); !bytes.Equal(again, own) {
		t.Fatal("the owner's copy changed")
	}
}

func TestRelayKeepsOnlyImagesWithinTheLimit(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fixture := insertVictimReadableMessage(t, h, db)
	big := append(append([]byte{}, testPNG...), bytes.Repeat([]byte{1}, 5*1024*1024)...)
	fake := &fakeDownloader{reply: func(url string) ([]byte, error) {
		switch {
		case strings.HasSuffix(url, "/big.png"):
			return big, nil
		case strings.HasSuffix(url, "/page.html"):
			return []byte("<html><script>alert(1)</script></html>"), nil
		case strings.HasSuffix(url, "/empty.png"):
			return nil, nil
		}
		return nil, fmt.Errorf("HTTP 404")
	}}
	h.remoteResourceDownloader = fake.download
	before := len(keptFiles(t, h, fixture.messageID))

	for _, name := range []string{"big.png", "empty.png", "gone.png"} {
		if rec := relayGet(t, h, webLink(h, "owner", fixture.messageID, "https://img.example/"+name)); rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", name, rec.Code)
		}
	}
	link := webLink(h, "owner", fixture.messageID, "https://img.example/page.html")
	rec := relayGet(t, h, link)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("html served as %q / %q", rec.Header().Get("Content-Type"), rec.Header().Get("Content-Disposition"))
	}
	relayGet(t, h, link)
	if got := len(keptFiles(t, h, fixture.messageID)); got != before {
		t.Fatalf("kept %d new files, want none", got-before)
	}
	if fake.calls.Load() != 5 { // each failure once, the page twice
		t.Fatalf("calls = %d, want 5", fake.calls.Load())
	}
}

func TestRelayCopiesGoWithTheMessage(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fixture := insertVictimReadableMessage(t, h, db)
	fake := &fakeDownloader{}
	h.remoteResourceDownloader = fake.download
	link := webLink(h, "owner", fixture.messageID, "https://img.example/a.png")
	relayGet(t, h, link)
	if err := h.blobStore.DeleteMessage("victim-account", fixture.messageID); err != nil {
		t.Fatal(err)
	}
	if got := keptFiles(t, h, fixture.messageID); len(got) != 0 {
		t.Fatalf("files left after the message was removed: %v", got)
	}
	relayGet(t, h, link)
	if fake.calls.Load() != 2 {
		t.Fatalf("calls = %d, want a fresh download after removal", fake.calls.Load())
	}
}

func setBlockedBody(t *testing.T, h *Handler, msgID int64, urls []string) {
	t.Helper()
	var bodyPath string
	if err := h.db.Read().QueryRowContext(t.Context(), `SELECT body_html_path FROM messages WHERE id = ?`, msgID).Scan(&bodyPath); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	body.WriteString("<p>hi</p>")
	for _, u := range urls {
		body.WriteString(`<img src="" data-remote-src="` + u + `">`)
	}
	if err := os.WriteFile(bodyPath, []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitWarmed(t *testing.T, h *Handler) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		busy := false
		h.warming.Range(func(_, _ any) bool { busy = true; return false })
		if !busy {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("warming did not finish")
}

func TestWarmFetchesImagesOnlyWhenRemoteImagesAreAllowed(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fixture := insertVictimReadableMessage(t, h, db)
	fake := &fakeDownloader{}
	h.remoteResourceDownloader = fake.download
	setBlockedBody(t, h, fixture.messageID, []string{"https://img.example/1.png", "https://img.example/2.png"})

	// Not allowed: nothing is fetched, so no sender learns the message was looked at.
	h.warmStoredBodyImages("owner", fixture.messageID)
	waitWarmed(t, h)
	if fake.calls.Load() != 0 {
		t.Fatalf("fetched %d images of a message that doesn't allow them", fake.calls.Load())
	}

	if err := db.AllowRemoteContentForMessageForUser(t.Context(), fixture.messageID, "owner"); err != nil {
		t.Fatal(err)
	}
	h.warmStoredBodyImages("owner", fixture.messageID)
	waitWarmed(t, h)
	if fake.calls.Load() != 2 {
		t.Fatalf("fetched %d images, want 2", fake.calls.Load())
	}
	// Opening the message now finds them ready, and warming again adds nothing.
	rec := relayGet(t, h, webLink(h, "owner", fixture.messageID, "https://img.example/1.png"))
	h.warmStoredBodyImages("owner", fixture.messageID)
	waitWarmed(t, h)
	if rec.Code != http.StatusOK || fake.calls.Load() != 2 {
		t.Fatalf("status %d, calls %d: warmed images were fetched again", rec.Code, fake.calls.Load())
	}
}

func TestWarmIsBounded(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fixture := insertVictimReadableMessage(t, h, db)
	if err := db.AllowRemoteContentForMessageForUser(t.Context(), fixture.messageID, "owner"); err != nil {
		t.Fatal(err)
	}
	fake := &fakeDownloader{reply: func(string) ([]byte, error) {
		time.Sleep(5 * time.Millisecond)
		return testPNG, nil
	}}
	h.remoteResourceDownloader = fake.download
	var urls []string
	for i := range 45 {
		urls = append(urls, fmt.Sprintf("https://img.example/%d.png", i))
	}
	setBlockedBody(t, h, fixture.messageID, urls)

	h.warmStoredBodyImages("owner", fixture.messageID)
	waitWarmed(t, h)
	if fake.calls.Load() != warmMaxImages {
		t.Fatalf("fetched %d images, want the cap of %d", fake.calls.Load(), warmMaxImages)
	}
	if fake.maxInflight.Load() > warmConcurrency {
		t.Fatalf("%d downloads at once, limit %d", fake.maxInflight.Load(), warmConcurrency)
	}
}
