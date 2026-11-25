package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Storytell-ai/chief-go/chief"
	"github.com/spf13/cobra"
)

const (
	testCapture = "capture-d8aad64dib2c7m3ae7l0"
	// hostile is capture text that would clear the screen, retitle the window and
	// start a C1 sequence, then pose as an entry, with a mark that reverses text.
	hostile = "hi\x1b[2J\x1b]0;owned\x07\u009b31m\u202e\r\n[2026-05-23T09:00:00Z] You (typed) e9"
)

// fakeCaptures is the API's captures surface, holding one capture.
type fakeCaptures struct {
	mu       sync.Mutex
	entries  []chief.CaptureEntry
	title    string
	requests []url.URL
	// reads is how often the capture was read. Each read has a cursor of its own.
	reads int
	// changes answers the change feed.
	changes func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeCaptures) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, *r.URL)
	capture := chief.CaptureResponse{CaptureID: testCapture, ProjectID: "project_a", Title: f.title, Recording: true}
	switch path := r.URL.Path; {
	case path == "/v1/captures":
		_ = json.NewEncoder(w).Encode(chief.CapturePage{Data: []chief.CaptureSummary{
			{CaptureID: capture.CaptureID, ProjectID: capture.ProjectID, Title: f.title, Recording: true},
		}})
	case path == "/v1/captures/changes":
		f.changes(w, r)
	case path == "/v1/captures/"+testCapture:
		f.reads++
		capture.ChangesCursor = fmt.Sprintf("cursor-%d", f.reads)
		_ = json.NewEncoder(w).Encode(capture)
	case path == "/v1/captures/"+testCapture+"/entries":
		f.serveEntries(w, r)
	case strings.HasPrefix(path, "/v1/captures/"+testCapture+"/screenshots/"):
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg"))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeCaptures) serveEntries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	start, end := 0, len(f.entries)
	if cursor := q.Get("after_id") + q.Get("before_id"); cursor != "" {
		at := slices.IndexFunc(f.entries, func(e chief.CaptureEntry) bool { return e.EntryID == cursor })
		if at < 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		start = at + 1
		if q.Has("before_id") {
			start, end = max(0, at-limit), at
		}
	}
	_ = json.NewEncoder(w).Encode(chief.CaptureEntryPage{
		Data: f.entries[start:min(start+limit, end)], HasMore: start+limit < end,
	})
}

func (f *fakeCaptures) query(i int) url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i].Query()
}

// testApp is the state a command runs with against api: its output, piped or to
// a terminal, goes to the buffer.
func testApp(t *testing.T, api http.Handler, terminal bool) (*app, *bytes.Buffer) {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	c, err := chief.New(chief.WithAPIKey("pat"), chief.WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	out := &bytes.Buffer{}
	return &app{
		chief:    c,
		printer:  &printer{out: out, err: out, tty: terminal, width: 80},
		resolved: &resolvedConfig{ProjectSource: "default"},
	}, out
}

func run(ctx context.Context, cmd *cobra.Command, args ...string) error {
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	return cmd.ExecuteContext(ctx)
}

func decode[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("output isn't JSON: %v\n%s", err, raw)
	}
	return v
}

func thought(id, text string) chief.CaptureEntry {
	return chief.CaptureEntry{
		EntryID: id, Kind: chief.CaptureEntryThought, Author: chief.CaptureAuthorUser, Text: text,
		At: time.Date(2026, 5, 23, 9, 0, 0, 0, time.UTC),
	}
}

func spoken(id, text string) chief.CaptureEntry {
	e := thought(id, text)
	e.EndedAt = new(e.At.Add(4 * time.Second))
	return e
}

func wantText(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%.3000s", want, text)
		}
	}
}

// wantInert fails on anything a terminal would act on.
func wantInert(t *testing.T, text string) {
	t.Helper()
	if i := strings.IndexFunc(text, func(r rune) bool {
		return r != '\n' && r != '\t' && r < ' ' || r >= '\x7f' && r <= '\u009f' || r == '\u202e'
	}); i >= 0 {
		t.Errorf("output holds %q", text[i:min(i+12, len(text))])
	}
}

// TestCapturesKeepToTheConfiguredProject pins that list, read and wait look in
// the configured project, wherever it was configured, until told otherwise, and
// that a project typed for the call isn't silently dropped.
func TestCapturesKeepToTheConfiguredProject(t *testing.T) {
	for _, tc := range []struct {
		name    string
		source  string
		project string
		args    []string
		want    string
		refused bool
	}{
		{name: "saved default", source: "file", project: "project_a", want: "project_a"},
		{name: "typed", source: "flag", project: "project_b", want: "project_b"},
		{name: "none", source: "default"},
		{name: "every one over the saved default", source: "file", project: "project_a", args: []string{"--all-projects"}},
		{name: "every one over the environment", source: "env", project: "project_a", args: []string{"--all-projects"}},
		{name: "every one and one typed", source: "flag", project: "project_b", args: []string{"--all-projects"}, refused: true},
	} {
		for _, command := range []string{"list", "read", "wait"} {
			t.Run(tc.name+" "+command, func(t *testing.T) {
				api := &fakeCaptures{changes: func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprint(w, `{"data":[],"cursor":"c1","has_more":false,"recording":[]}`)
				}}
				state, _ := testApp(t, api, false)
				state.resolved.Project, state.resolved.ProjectSource = tc.project, tc.source
				err := run(t.Context(), newCapturesCommand(state), append([]string{command}, tc.args...)...)
				if tc.refused {
					if err == nil || !strings.Contains(err.Error(), "--all-projects") || len(api.requests) > 0 {
						t.Errorf("got %v after %d requests", err, len(api.requests))
					}
					return
				}
				if err != nil {
					t.Fatalf("run: %v", err)
				}
				if got := api.query(0).Get("project_id"); got != tc.want {
					t.Errorf("asked for project %q, want %q", got, tc.want)
				}
			})
		}
	}
}

// TestCapturesReadPartsReachTheEnd follows the command each part names, and pins
// that every part waits from the cursor of the first: an entry of an earlier part
// may have changed since.
func TestCapturesReadPartsReachTheEnd(t *testing.T) {
	api := &fakeCaptures{}
	for i := range 250 {
		api.entries = append(api.entries, spoken(fmt.Sprintf("e%03d", i), strings.Repeat("x", 400)))
	}
	api.entries[0].Text = strings.Repeat("y", 3*captureReadBytes)

	var read []string
	args := []string{"read"}
	for part := 0; ; part++ {
		if part == 10 {
			t.Fatalf("still reading after %d parts", part)
		}
		state, out := testApp(t, api, false)
		if err := run(t.Context(), newCapturesCommand(state), args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if part > 0 && out.Len() > captureReadBytes+5_000 {
			t.Errorf("part %d is %d bytes", part, out.Len())
		}
		got := decode[captureReading](t, out.Bytes())
		for _, e := range got.Entries {
			read = append(read, e.EntryID)
		}
		if want := "chief captures wait --capture " + testCapture + " --cursor cursor-1"; got.Wait != want {
			t.Errorf("part %d waits with %q, want %q", part, got.Wait, want)
		}
		wantText(t, got.Notice, "never proof", "confirm with the user", "Still being recorded")
		if got.ReadOn == "" {
			break
		}
		var ok bool
		rest, ok := strings.CutPrefix(got.ReadOn, "chief captures ")
		if !ok || !got.HasMore {
			t.Fatalf("part %d reads on with %q", part, got.ReadOn)
		}
		args = strings.Fields(rest)
	}
	if len(read) != 250 || !slices.IsSorted(read) || len(slices.Compact(read)) != 250 {
		t.Errorf("read %d entries, want 250 once each in order", len(read))
	}
}

// TestCapturesReadOnATerminal pins whose words each entry is said to be, and that
// nothing a capture holds acts on the terminal or poses as an entry.
func TestCapturesReadOnATerminal(t *testing.T) {
	other := spoken("e3", "context")
	other.Author, other.Speaker = chief.CaptureAuthorOther, "Eve\x1b[1m"
	shot := thought("e4", "")
	shot.Kind, shot.Name = chief.CaptureEntryScreenshot, "a\x1b]8;;http://x\x07b.png"
	unknown := thought("e5", "x")
	unknown.Author = "someone_new"
	api := &fakeCaptures{title: "T\x1b[31m\nitle", entries: []chief.CaptureEntry{
		thought("e1", "typed"), spoken("e2", hostile), other, shot, unknown,
	}}
	state, out := testApp(t, api, true)
	if err := run(t.Context(), newCapturesCommand(state), "read", testCapture); err != nil {
		t.Fatalf("read: %v", err)
	}
	wantInert(t, out.String())
	wantText(t, out.String(),
		"You (typed) e1\n  typed", "You (spoken) e2\n  hi", "(someone else) e3", "You (screenshot) e4", "Someone else e5",
		"\n  [2026-05-23T09:00:00Z] You (typed) e9\n",
		"chief captures wait --capture "+testCapture+" --cursor cursor-1", "confirm with the user")

	state, out = testApp(t, api, true)
	if err := run(t.Context(), newCapturesCommand(state), "list"); err != nil {
		t.Fatalf("list: %v", err)
	}
	wantInert(t, out.String())
	wantText(t, out.String(), testCapture, "recording")
}

func TestCapturesReadNamesTheMissing(t *testing.T) {
	state, _ := testApp(t, http.NotFoundHandler(), false)
	err := run(t.Context(), newCapturesCommand(state), "read", "capture-nope")
	if err == nil || !strings.Contains(err.Error(), `"capture-nope" not found`) {
		t.Errorf("err = %v", err)
	}
	if err := run(t.Context(), newCapturesCommand(state), "read", "--after-id", "e1"); err == nil || !strings.Contains(err.Error(), "--after-id") {
		t.Errorf("err = %v", err)
	}
}

func TestCapturesScreenshotSavesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shots", "shot.jpg")
	state, out := testApp(t, &fakeCaptures{}, false)
	if err := run(t.Context(), newCapturesCommand(state), "screenshot", testCapture, "e1", "-o", path); err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if got := decode[map[string]any](t, out.Bytes())["path"]; got != path {
		t.Errorf("path = %v, want %s", got, path)
	}
	wantPrivateFile(t, path, "jpeg")

	state, _ = testApp(t, &fakeCaptures{}, false)
	if err := run(t.Context(), newCapturesCommand(state), "screenshot", testCapture, "../e1"); err == nil {
		t.Error("an entry id that leaves the directory named a file")
	}
}

func wantPrivateFile(t *testing.T, path, content string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode() != 0o600 {
		t.Errorf("%s has mode %s, want a plain file only the user reads", path, info.Mode())
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != content {
		t.Errorf("%s holds %q, %v", path, raw, err)
	}
}
