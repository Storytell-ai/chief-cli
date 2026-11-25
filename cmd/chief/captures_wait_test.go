package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Storytell-ai/chief-go/chief"
)

// feed answers the change feed with page, whatever it is asked.
func feed(page chief.CaptureChangesResponse) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(page) }
}

func changed(entries ...chief.CaptureEntry) []chief.CaptureChange {
	change := chief.CaptureChange{CaptureID: testCapture, ProjectID: "project_a", Title: "Ideas"}
	for _, e := range entries {
		change.Entries = append(change.Entries, chief.CaptureEntryChange{CaptureEntry: e})
	}
	return []chief.CaptureChange{change}
}

func screenshotEntry(id string) chief.CaptureEntry {
	e := thought(id, "")
	e.Kind, e.Name = chief.CaptureEntryScreenshot, "../../evil.png"
	return e
}

func TestCapturesWaitPrintsJSONWhenPiped(t *testing.T) {
	recording := []string{testCapture}
	for _, tc := range []struct {
		name   string
		answer chief.CaptureChangesResponse
		args   []string
		next   string
		notice string
		// held is how many entries the wait keeps back, with the cursor, until
		// recording stops.
		held int
	}{
		{
			name:   "nothing recording",
			answer: chief.CaptureChangesResponse{Cursor: "c2", Recording: []string{}, Data: changed(spoken("e1", "hi"))},
			args:   []string{"--cursor", "c1"},
			next:   "chief captures wait --cursor c2 --live", notice: "confirm with the user",
		},
		{
			name:   "timed out with nothing",
			answer: chief.CaptureChangesResponse{Cursor: "c1", Recording: recording},
			args:   []string{"--capture", testCapture, "--timeout", "200ms"},
			next:   "chief captures wait --cursor c1 --capture " + testCapture + " --timeout 200ms",
		},
		{
			name:   "timed out while recording",
			answer: chief.CaptureChangesResponse{Cursor: "c2", Recording: recording, Data: changed(spoken("e1", "hi"))},
			args:   []string{"--cursor", "c1", "--all-projects", "--timeout", "200ms"},
			next:   "chief captures wait --cursor c1 --all-projects --timeout 200ms", held: 1,
		},
		{
			name:   "live, still recording",
			answer: chief.CaptureChangesResponse{Cursor: "c2", Recording: recording, Data: changed(spoken("e1", "hi"))},
			args:   []string{"--cursor", "c1", "--live", "--timeout", "200ms"},
			next:   "chief captures wait --cursor c2 --live --timeout 200ms", notice: "do not act",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, out := testApp(t, &fakeCaptures{changes: feed(tc.answer)}, false)
			if err := run(t.Context(), newCapturesCommand(state), append([]string{"wait"}, tc.args...)...); err != nil {
				t.Fatalf("wait: %v", err)
			}
			got := decode[captureUpdates](t, out.Bytes())
			cursor, changes := tc.answer.Cursor, len(tc.answer.Data)
			if tc.held > 0 {
				cursor, changes = "c1", 0
			}
			if got.Next != tc.next || got.Cursor != cursor || len(got.Data) != changes || got.Held != tc.held ||
				!slices.Equal(got.Recording, tc.answer.Recording) {
				t.Errorf("got %+v of %+v, want next %q", got, got.CaptureChangesResponse, tc.next)
			}
			wantText(t, got.Notice, tc.notice)
			if tc.notice == "" && got.Notice != "" {
				t.Errorf("an empty result says %q", got.Notice)
			}
			if !strings.Contains(out.String(), `"data":[`) || strings.Count(out.String(), "\n") != 1 {
				t.Errorf("output isn't one line with a list: %s", out)
			}
		})
	}
}

func TestCapturesWaitPastTheHorizonSaysToReadAgain(t *testing.T) {
	state, _ := testApp(t, &fakeCaptures{changes: func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}}, false)
	err := run(t.Context(), newCapturesCommand(state), "wait", "--cursor", "old")
	if err == nil || !strings.Contains(err.Error(), "chief captures read") {
		t.Errorf("err = %v", err)
	}
}

// TestCapturesWaitEndsCleanlyWhenInterrupted pins that Ctrl-C, which ends the
// command's context, prints the cursor that goes on and no words that may still
// change speaker.
func TestCapturesWaitEndsCleanlyWhenInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	state, out := testApp(t, &fakeCaptures{changes: func(w http.ResponseWriter, r *http.Request) {
		feed(chief.CaptureChangesResponse{Cursor: "c2", Recording: []string{testCapture}, Data: changed(spoken("e1", "hi"))})(w, r)
		time.AfterFunc(200*time.Millisecond, cancel)
	}}, false)
	if err := run(ctx, newCapturesCommand(state), "wait", "--cursor", "c1"); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got := decode[captureUpdates](t, out.Bytes()); got.Cursor != "c1" || len(got.Data) != 0 || got.Held != 1 {
		t.Errorf("got %s", out)
	}
}

func TestCapturesWaitOnATerminal(t *testing.T) {
	state, out := testApp(t, &fakeCaptures{changes: feed(chief.CaptureChangesResponse{
		Cursor: "c2", Recording: []string{}, Data: changed(spoken("e1", hostile)),
	})}, true)
	if err := run(t.Context(), newCapturesCommand(state), "wait", "--cursor", "c1"); err != nil {
		t.Fatalf("wait: %v", err)
	}
	wantInert(t, out.String())
	wantText(t, out.String(), "Ideas", "You (spoken) e1\n  hi", "confirm with the user", "chief captures wait --cursor c2")

	state, out = testApp(t, &fakeCaptures{changes: feed(chief.CaptureChangesResponse{
		Cursor: "c2", Recording: []string{}, Data: changed(spoken("e1", hostile)),
	})}, true)
	state.printer.json = true
	if err := run(t.Context(), newCapturesCommand(state), "wait", "--cursor", "c1"); err != nil {
		t.Fatalf("wait --json: %v", err)
	}
	if got := decode[captureUpdates](t, out.Bytes()); got.Data[0].Entries[0].Text != hostile {
		t.Errorf("JSON holds %q, want the text unchanged", got.Data[0].Entries[0].Text)
	}
	for _, r := range out.String() {
		if r == '\x1b' || r == '\u009b' {
			t.Errorf("JSON on a terminal holds %q", r)
		}
	}
}

// TestCapturesFollow pins the stream an agent reads line by line: a failing API
// costs nothing, a cursor too old is said to be and started over, and the end of
// recording shows with no change to carry it.
func TestCapturesFollow(t *testing.T) {
	recording := []string{testCapture}
	answers := []struct {
		status int
		page   chief.CaptureChangesResponse
	}{
		{page: chief.CaptureChangesResponse{Cursor: "c2", Recording: recording, Data: changed(spoken("e1", "one"))}},
		{status: http.StatusBadGateway},
		{page: chief.CaptureChangesResponse{Cursor: "c2", Recording: recording}},
		{status: http.StatusGone},
		{page: chief.CaptureChangesResponse{Cursor: "c9", Recording: recording}},
		{page: chief.CaptureChangesResponse{Cursor: "c10", Recording: recording, Data: changed(spoken("e2", "two"))}},
		{page: chief.CaptureChangesResponse{Cursor: "c10", Recording: []string{}}},
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	n := 0
	api := &fakeCaptures{changes: func(w http.ResponseWriter, _ *http.Request) {
		if n == len(answers) {
			cancel()
			n--
		}
		answer := answers[n]
		n++
		if answer.status != 0 {
			w.WriteHeader(answer.status)
			return
		}
		_ = json.NewEncoder(w).Encode(answer.page)
	}}
	state, out := testApp(t, api, false)
	// Each wait is long enough for one poll and no second.
	f := &captureWaitFlags{cursor: "c1", follow: true}
	if err := runCapturesWait(ctx, state, f, 300*time.Millisecond); err != nil {
		t.Fatalf("follow: %v", err)
	}

	var got []string
	for line := range bytes.Lines(out.Bytes()) {
		u := decode[struct {
			Notice    string                `json:"notice"`
			Cursor    string                `json:"cursor"`
			Data      []chief.CaptureChange `json:"data"`
			Recording []string              `json:"recording"`
		}](t, line)
		got = append(got, fmt.Sprintf("%s %d %d missed=%t provisional=%t settled=%t", u.Cursor, len(u.Data), len(u.Recording),
			strings.Contains(u.Notice, "missed"), strings.Contains(u.Notice, "do not act"), strings.Contains(u.Notice, "should have settled")))
	}
	if want := []string{
		"c2 1 1 missed=false provisional=true settled=false",
		" 0 0 missed=true provisional=false settled=false",
		"c10 1 1 missed=false provisional=true settled=false",
		"c10 0 0 missed=false provisional=false settled=true",
	}; !slices.Equal(got, want) {
		t.Errorf("printed %q, want %q\n%s", got, want, out)
	}
	var cursors []string
	for i := range 7 {
		cursors = append(cursors, api.query(i).Get("cursor"))
	}
	if want := []string{"c1", "c2", "c2", "c2", "", "c9", "c10"}; !slices.Equal(cursors, want) {
		t.Errorf("asked from %q, want %q", cursors, want)
	}
}

func TestCapturesFollowReturnsWhatNeverPasses(t *testing.T) {
	state, _ := testApp(t, &fakeCaptures{changes: func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}}, false)
	if err := run(t.Context(), newCapturesCommand(state), "wait", "--follow"); !chief.IsUnauthorized(err) {
		t.Errorf("err = %v", err)
	}
}

// TestCapturesWaitSavesScreenshots pins that a file is named by its entry's id
// alone, kept to the directory and to the user, and that one that can't be saved
// is reported rather than lost.
func TestCapturesWaitSavesScreenshots(t *testing.T) {
	dir, outside := t.TempDir(), filepath.Join(t.TempDir(), "outside.jpg")
	if err := os.WriteFile(outside, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seen.jpg"), []byte("an earlier, longer one"), 0o644); err != nil {
		t.Fatal(err)
	}
	page := chief.CaptureChangesResponse{Cursor: "c2", Recording: []string{}, Data: changed(
		screenshotEntry("e1"), screenshotEntry("linked"), screenshotEntry("seen"),
		screenshotEntry("../escape"), screenshotEntry("/tmp/escape"), screenshotEntry("a/b"), screenshotEntry(""), thought("e2", "x"),
	)}
	page.Data[0].Entries = append(page.Data[0].Entries,
		chief.CaptureEntryChange{CaptureEntry: screenshotEntry("gone"), Deleted: true})
	state, out := testApp(t, &fakeCaptures{changes: feed(page)}, false)
	if err := run(t.Context(), newCapturesCommand(state), "wait", "--cursor", "c1", "--screenshots", dir); err != nil {
		t.Fatalf("wait: %v", err)
	}

	for _, name := range []string{"e1.jpg", "linked.jpg", "seen.jpg"} {
		wantPrivateFile(t, filepath.Join(dir, name), "jpeg")
	}
	if raw, err := os.ReadFile(outside); err != nil || string(raw) != "mine" {
		t.Errorf("the file a link led to holds %q, %v", raw, err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 3 {
		t.Errorf("saved %v, %v; want three files", files, err)
	}
	var saved, failed int
	for _, s := range decode[captureUpdates](t, out.Bytes()).Screenshots {
		switch {
		case s.Error != "" && s.Path == "":
			failed++
		case s.Error == "" && filepath.Dir(s.Path) == dir:
			saved++
		}
	}
	if saved != 3 || failed != 4 {
		t.Errorf("reported %d saved and %d failed, want 3 and 4: %s", saved, failed, out)
	}
}
