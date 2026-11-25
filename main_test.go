package main

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// fake stands in for the fsearch daemon.
type fake struct {
	hits     []Hit
	files    []GrepFile
	err      error
	searches atomic.Int32
	lastQ    atomic.Value
}

func (f *fake) Search(q string, limit int) (NameResult, error) {
	f.searches.Add(1)
	f.lastQ.Store(q)
	return NameResult{Hits: f.hits, TookUS: 120}, f.err
}

func (f *fake) Grep(q string, limit int) (GrepResult, error) {
	f.searches.Add(1)
	f.lastQ.Store(q)
	return GrepResult{Files: f.files, Complete: true}, f.err
}

func (f *fake) Status() (Status, error) { return Status{Entries: 8_300_000}, nil }

func sampleHits() []Hit {
	return []Hit{
		{Kind: "file", Path: "/Users/ada/Developer/fsearch/src/main.rs", Size: 4096, MTime: 1_760_000_000},
		{Kind: "file", Path: "/Users/ada/Developer/other/main.go", Size: 100, MTime: 1_750_000_000},
		{Kind: "dir", Path: "/Users/ada/Documents/main", MTime: 1_740_000_000},
	}
}

// settle runs the debounce and the search to the end.
func settle(t *testing.T, tt *ui.Tester, a *app) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		tt.Frame()
		if a.idle() {
			return
		}
	}
	t.Fatal("search did not finish")
}

func TestSearchByName(t *testing.T) {
	f := &fake{hits: sampleHits()}
	a := newApp(f)
	tt := ui.NewTester(a.view, 980, 640)

	if !tt.HasText("Search every file on this Mac") {
		t.Fatalf("no hint on an empty query: %q", tt.Texts())
	}
	if err := tt.Click("Search files"); err != nil {
		t.Fatal(err)
	}
	tt.Type("main")
	settle(t, tt, a)

	if got := f.lastQ.Load(); got != "main" {
		t.Errorf("searched %v, want main", got)
	}
	for _, want := range []string{"main.rs", "main.go", "3 files", "4.0 KB"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
}

// The empty window is the only cheat sheet many users see, so keep the
// README's query examples and rules here as well.
func TestHintCoversFsearchSyntax(t *testing.T) {
	a := newApp(&fake{})
	tt := ui.NewTester(a.view, 980, 640)
	tt.Frame()
	for _, want := range []string{
		"fsearch 'readme in:~/Developer'",
		"type:image size:>5mb mtime:<7d",
		"ext:rs regex:fn\\s+\\w+_dir",
		"sym:apply_dir",
		"src/**/*.{c,h} !test/ init",
		"git:modified in:~/Developer/app",
		"mian.rs",
		"Content mode is smart-case",
		"removes its Dock icon",
	} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in hint: %q", want, tt.Texts())
		}
	}
}

func TestTypingSearchesOnce(t *testing.T) {
	f := &fake{hits: sampleHits()}
	a := newApp(f)
	tt := ui.NewTester(a.view, 980, 640)
	if err := tt.Click("Search files"); err != nil {
		t.Fatal(err)
	}
	for _, ch := range "main" {
		tt.Type(string(ch))
	}
	settle(t, tt, a)
	if n := f.searches.Load(); n != 1 {
		t.Errorf("%d searches for one burst of typing, want 1", n)
	}
}

func TestStaleAnswerIsDropped(t *testing.T) {
	a := newApp(&fake{hits: sampleHits()})
	a.query = "main"
	a.startSearch()
	a.clear() // the user emptied the box before the answer came
	time.Sleep(100 * time.Millisecond)
	a.drain()
	if len(a.rows) != 0 {
		t.Errorf("a stale answer filled %d rows", len(a.rows))
	}
}

func TestContentMode(t *testing.T) {
	f := &fake{files: []GrepFile{{
		Path:    "/Users/ada/Developer/fsearch/src/server.rs",
		Matches: []GrepMatch{{Line: 162, Text: "    let op = v.get(\"op\")"}},
	}}}
	a := newApp(f)
	a.mode = modeContent
	tt := ui.NewTester(a.view, 980, 640)
	if err := tt.Click("Search files"); err != nil {
		t.Fatal(err)
	}
	tt.Type("ext:rs op")
	settle(t, tt, a)
	for _, want := range []string{"server.rs", "162", "1 matches"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
}

// Several matches in one file make several rows with the same path. MyGo
// requires distinct element keys, so the table cannot key rows by path.
func TestContentModeDuplicatePaths(t *testing.T) {
	f := &fake{files: []GrepFile{{
		Path: "/Users/ada/a.go",
		Matches: []GrepMatch{
			{Line: 1, Text: "first"},
			{Line: 2, Text: "second"},
		},
	}}}
	a := newApp(f)
	a.mode = modeContent
	tt := ui.NewTester(a.view, 980, 640)
	if err := tt.Click("Search files"); err != nil {
		t.Fatal(err)
	}
	tt.Type("first")
	settle(t, tt, a)
	if len(a.sorted) != 2 {
		t.Fatalf("%d rows for two matches in one file, want 2", len(a.sorted))
	}
}

// Secondary columns must switch to AccentText on a highlighted row: writing
// a fixed TextMuted there leaves dark text on MyGo's blue selected row.
func TestMutedColorOnSelectedRow(t *testing.T) {
	a := newApp(&fake{})
	a.sorted = []row{{Key: "a", Path: "/a"}, {Key: "b", Path: "/b"}}
	a.chosen.Add("b")

	tt := ui.NewTester(a.view, 980, 640)
	tt.Frame()
	tm := ui.LightTheme()
	wantMuted, wantAccent := tm.TextMuted, tm.AccentText
	if got := a.mutedColor(tm, "a"); got != wantMuted {
		t.Errorf("unselected row color = %v, want %v", got, wantMuted)
	}
	if got := a.mutedColor(tm, "b"); got != wantAccent {
		t.Errorf("selected row color = %v, want %v", got, wantAccent)
	}
}

func TestIndexingNotice(t *testing.T) {
	f := &fake{err: &IndexingError{Message: "indexing: 2.1M entries so far"}}
	a := newApp(f)
	tt := ui.NewTester(a.view, 980, 640)
	if err := tt.Click("Search files"); err != nil {
		t.Fatal(err)
	}
	tt.Type("main")
	settle(t, tt, a)
	if !tt.HasText("indexing: 2.1M entries so far") {
		t.Errorf("texts %q", tt.Texts())
	}
}

func TestError(t *testing.T) {
	a := newApp(&fake{err: errors.New("bad query")})
	tt := ui.NewTester(a.view, 980, 640)
	if err := tt.Click("Search files"); err != nil {
		t.Fatal(err)
	}
	tt.Type("main")
	settle(t, tt, a)
	if !tt.HasText("bad query") {
		t.Errorf("texts %q", tt.Texts())
	}
}

func TestSort(t *testing.T) {
	a := newApp(&fake{})
	a.rows = nameRows(NameResult{Hits: sampleHits()})
	a.sort = ui.SortOrder{Column: "size"}
	a.resort()
	if a.sorted[0].Size != 0 || a.sorted[2].Size != 4096 {
		t.Errorf("ascending by size: %+v", a.sorted)
	}
	a.sort.Descending = true
	a.resort()
	if a.sorted[0].Size != 4096 {
		t.Errorf("descending by size: %+v", a.sorted)
	}
}

// MyGo's header click only toggles ascending/descending; Relevance is how the
// user gets back to the ranking fsearch returned.
func TestRelevanceRestoresSearchOrder(t *testing.T) {
	a := newApp(&fake{})
	a.query = "main"
	a.rows = nameRows(NameResult{Hits: sampleHits()})
	a.sort = ui.SortOrder{Column: "name"}
	a.resort()

	tt := ui.NewTester(a.view, 980, 640)
	tt.Frame()
	if err := tt.Click("Relevance"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if a.sort != (ui.SortOrder{}) {
		t.Errorf("sort after Relevance = %+v, want none", a.sort)
	}
	if len(a.sorted) != len(a.rows) {
		t.Fatalf("%d rows after Relevance, want %d", len(a.sorted), len(a.rows))
	}
	for i := range a.rows {
		if a.sorted[i].Key != a.rows[i].Key {
			t.Fatalf("row %d after Relevance = %s, want %s", i, a.sorted[i].Key, a.rows[i].Key)
		}
	}
}

func TestRelevanceTooltipExplainsReset(t *testing.T) {
	a := newApp(&fake{})
	a.rows = nameRows(NameResult{Hits: sampleHits()})
	a.sort = ui.SortOrder{Column: "name"}
	a.resort()

	tt := ui.NewTester(a.view, 980, 640)
	tt.Frame()
	r, ok := tt.Find("Relevance")
	if !ok {
		t.Fatal("Relevance button not found")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(700 * time.Millisecond)
	if !tt.HasText("Reset sort: back to fsearch's relevance order") {
		t.Fatalf("Relevance tooltip missing; texts: %q", tt.Texts())
	}
}

func TestSplitGrep(t *testing.T) {
	p, f := splitGrep("ext:rs in:~/Developer apply_dir")
	if p != "apply_dir" || f != "ext:rs in:~/Developer" {
		t.Errorf("got %q, %q", p, f)
	}
	p, f = splitGrep("a:b plain")
	if p != "a:b plain" || f != "" {
		t.Errorf("unknown key kept as text: %q, %q", p, f)
	}
}

func TestFormat(t *testing.T) {
	for in, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 5 << 20: "5.0 MB"} {
		if got := formatSize(in); got != want {
			t.Errorf("formatSize(%d) = %q, want %q", in, got, want)
		}
	}
	if got := formatCount(8_300_000); !strings.HasPrefix(got, "8.3") {
		t.Errorf("formatCount = %q", got)
	}
}

// TestSnapshot writes what the window shows to SNAPSHOT_DIR, to look at.
func TestSnapshot(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("SNAPSHOT_DIR not set")
	}
	a := newApp(&fake{hits: sampleHits()})
	tt := ui.NewTester(a.view, 980, 640)
	save := func(name string) {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	tt.Frame()
	save("empty.png")
	if err := tt.Click("Search files"); err != nil {
		t.Fatal(err)
	}
	tt.Type("main")
	settle(t, tt, a)
	save("results.png")
}
