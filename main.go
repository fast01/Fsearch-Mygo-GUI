package main

import (
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	modeName = iota
	modeContent
)

const (
	resultLimit = 500
	// debounce is how long typing pauses before a search starts, so a burst
	// of keystrokes costs one query.
	debounce = 120 * time.Millisecond
	// contentDebounce is longer: a content search reads files.
	contentDebounce = 350 * time.Millisecond
)

// row is one line of the results table: a file found by name, or one
// matching line of a file found by content.
type row struct {
	// Key is unique per row: Content mode can show several lines of the
	// same file, and MyGo requires distinct element keys for table rows.
	Key   string
	Path  string
	Dir   string
	Name  string
	Kind  string
	Size  int64
	MTime int64
	Line  int
	Text  string
}

// app is the state the window shows. The view reads it on the main thread;
// searches run elsewhere and change it through win.Update.
type app struct {
	searcher Searcher
	win      *mygo.Window

	query string
	mode  int

	rows     []row
	sorted   []row
	table    ui.ListState
	selected int
	chosen   ui.Selection[string]
	sort     ui.SortOrder

	// pending is when the query last changed and has not been searched yet.
	pending  time.Time
	searchID int
	busy     bool
	err      string
	// notice is a message that is not an error, such as "indexing…".
	notice string
	took   time.Duration
	status Status
	hasSt  bool

	// queued holds changes made without a window; see apply.
	queued []func()
	mu     sync.Mutex
}

func newApp(s Searcher) *app {
	a := &app{searcher: s, selected: -1}
	a.table.Selected = &a.selected
	a.table.Selection = &a.chosen
	a.table.Sort = &a.sort
	a.table.Key = func(i int) any {
		if i >= 0 && i < len(a.sorted) {
			return a.sorted[i].Key
		}
		return ""
	}
	a.table.Label = func(i int) string {
		if i >= 0 && i < len(a.sorted) {
			return a.sorted[i].Name
		}
		return ""
	}
	return a
}

func (a *app) columns() []ui.TableColumn {
	if a.mode == modeContent {
		return []ui.TableColumn{
			{Title: "Name", ID: "name", Width: 200, Sortable: true},
			{Title: "Line", ID: "line", Width: 60, Align: ui.End, Sortable: true},
			{Title: "Match", ID: "text"},
			{Title: "Folder", ID: "dir", Width: 260, Sortable: true},
		}
	}
	return []ui.TableColumn{
		{Title: "Name", ID: "name", Width: 260, MinWidth: 80, Sortable: true},
		{Title: "Folder", ID: "dir", Sortable: true},
		{Title: "Size", ID: "size", Width: 90, Align: ui.End, Sortable: true},
		{Title: "Modified", ID: "mtime", Width: 140, Sortable: true},
	}
}

func (a *app) view(c *ui.Context) {
	a.drain()
	a.tick(c)

	ui.Column(c).Fill().Children(func() {
		a.toolbar(c)
		ui.Divider(c)
		a.results(c).Grow(1)
		ui.Divider(c)
		a.statusBar(c)
	})
}

// tick starts a search once typing has paused, and asks for a frame to
// notice that moment when it is still ahead.
func (a *app) tick(c *ui.Context) {
	if a.pending.IsZero() {
		return
	}
	wait := debounce
	if a.mode == modeContent {
		wait = contentDebounce
	}
	if left := time.Until(a.pending.Add(wait)); left > 0 {
		c.After(left)
		return
	}
	a.pending = time.Time{}
	a.startSearch()
}

func (a *app) toolbar(c *ui.Context) ui.Element {
	return ui.Row(c).Padding(10).Gap(10).Children(func() {
		search := ui.SearchField(c.Key("search"), &a.query).Label("Search files").Grow(1)
		if search.Changed() {
			a.pending = time.Now()
			if strings.TrimSpace(a.query) == "" {
				a.clear()
			}
		}
		if search.Submitted() {
			a.pending = time.Time{}
			a.startSearch()
		}
		if ui.Segmented(c, &a.mode, "Name", "Content").Label("Search in").Changed() {
			a.clear()
			a.sort = ui.SortOrder{}
			a.pending = time.Now()
		}
	})
}

func (a *app) clear() {
	a.searchID++
	a.rows, a.sorted = nil, nil
	a.selected = -1
	a.chosen.Clear()
	a.err, a.notice, a.busy = "", "", false
}

func (a *app) results(c *ui.Context) ui.Element {
	if strings.TrimSpace(a.query) == "" {
		return a.hint(c)
	}
	if len(a.sorted) == 0 {
		return ui.Column(c).Center().Children(func() {
			switch {
			case a.err != "":
				ui.Text(c, a.err).TextColor(c.Theme().Danger)
			case a.notice != "":
				ui.Text(c, a.notice).TextColor(c.Theme().TextMuted)
			case a.busy:
				ui.Spinner(c)
			default:
				ui.Text(c, "No results").TextColor(c.Theme().TextMuted)
			}
		})
	}

	rows := a.sorted
	t := c.Theme()
	table := ui.Table(c, &a.table, a.columns(), len(rows), func(i, col int) {
		r := rows[i]
		muted := a.mutedColor(t, r.Key)
		if a.mode == modeContent {
			switch col {
			case 0:
				ui.Text(c, r.Name).SingleLine()
			case 1:
				ui.Textf(c, "%d", r.Line).TextColor(muted)
			case 2:
				ui.Text(c, strings.TrimSpace(r.Text)).SingleLine()
			case 3:
				ui.Text(c, r.Dir).SingleLine().TextColor(muted)
			}
			return
		}
		switch col {
		case 0:
			ui.Text(c, r.Name).SingleLine()
		case 1:
			ui.Text(c, r.Dir).SingleLine().TextColor(muted)
		case 2:
			if r.Kind == "dir" {
				ui.Text(c, "—").TextColor(muted)
			} else {
				ui.Text(c, formatSize(r.Size)).TextColor(muted)
			}
		case 3:
			ui.Text(c, formatTime(r.MTime)).TextColor(muted)
		}
	}).Label("Results")

	if table.Changed() && a.sortChanged() {
		a.resort()
	}
	if table.Submitted() {
		if p, ok := a.path(a.selected); ok {
			a.open(p)
		}
	}
	return table.ContextMenu(a.resultMenu)
}

// resultMenu fills m with the items acting on the chosen rows, which a
// right-click outside them reduces to the row clicked.
func (a *app) resultMenu(m *ui.Menu) {
	paths := a.chosenPaths()
	has := len(paths) > 0
	if m.Item("Open").Disabled(!has).Chosen() {
		for _, p := range paths {
			a.open(p)
		}
	}
	if m.Item("Reveal in Finder").Disabled(!has).Chosen() {
		for _, p := range paths {
			a.reveal(p)
		}
	}
	m.Separator()
	if m.Item("Copy Path").Disabled(!has).Chosen() {
		mygo.Clipboard.WriteText(strings.Join(paths, "\n"))
	}
}

// chosenPaths returns the paths of the rows chosen, in the order the
// table shows them.
// mutedColor is the color for secondary table text. On a highlighted row
// MyGo paints the cell with AccentText (white on blue); a fixed TextMuted
// would override that and stay dark, unreadable on the highlight.
func (a *app) mutedColor(t *ui.Theme, key string) ui.Color {
	if a.chosen.Has(key) {
		return t.AccentText
	}
	return t.TextMuted
}

func (a *app) chosenPaths() []string {
	var paths []string
	seen := make(map[string]bool)
	for _, r := range a.sorted {
		if a.chosen.Has(r.Key) && !seen[r.Path] {
			seen[r.Path] = true
			paths = append(paths, r.Path)
		}
	}
	return paths
}

func (a *app) hint(c *ui.Context) ui.Element {
	t := c.Theme()
	return ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).Padding(20, 32).Gap(8).Children(func() {
			ui.Text(c, "Search every file on this Mac").FontSize(20).Bold()
			ui.Text(c, "Type to search by name as you type. Words are fuzzy, and longer ones forgive a typo. Switch to Content to search inside indexed files.").TextColor(t.TextMuted).MaxWidth(760)
			ui.Text(c, "In Terminal, put a query in quotes after fsearch — fsearch 'readme in:~/Developer'. Here, just type the part in quotes.").TextColor(t.TextMuted).MaxWidth(760)

			ui.Divider(c)
			hintHeading(c, "QUICK ACCESS")
			hintRow(c, "⌘⇧Space", "show FSearch from anywhere (menu-bar icon also works)")
			ui.Text(c, "Closing this window hides it to the menu bar and removes its Dock icon; the Dock icon returns when the window shows again. The index keeps updating in the background. Choose Quit from the menu-bar icon to exit.").TextColor(t.TextMuted).MaxWidth(760)

			ui.Divider(c)
			hintHeading(c, "TRY IT")
			hintRow(c, "main", "fuzzy file name")
			hintRow(c, "mian.rs", "one typo is forgiven")
			hintRow(c, "readme in:~/Developer", "inside one folder")
			hintRow(c, "type:image size:>5mb mtime:<7d", "large images changed in the last week")
			hintRow(c, "sym:apply_dir", "where a symbol is defined")
			hintRow(c, "git:modified in:~/Developer/app", "changed files in a repo")
			hintRow(c, "ext:go func", "Content mode: text inside Go files")

			ui.Divider(c)
			hintHeading(c, "QUERY SYNTAX")
			hintRow(c, "ext:rs type:image kind:dir", "extension, file type, kind")
			hintRow(c, "in:~/Developer path:src/", "folder and path fragment")
			hintRow(c, "size:>10m mtime:<7d", "size and modified time")
			hintRow(c, "ext:rs regex:fn\\s+\\w+_dir", "extension and regex inside lines")
			hintRow(c, "re:^TODO grep:apply_dir", "regex on names, text in files")
			hintRow(c, "limit:20", "cap the number of results")
			hintRow(c, "'exact ^prefix suffix$", "exact, starts with, ends with")
			hintRow(c, "!test/ !exclude", "drop paths or words")

			ui.Divider(c)
			hintHeading(c, "MATCHING")
			ui.Text(c, "Words are fuzzy anywhere in a name; two typos are forgiven in names of 8+ letters, and several words can spell one name. Accented names match however they were stored.").TextColor(t.TextMuted).MaxWidth(760)

			ui.Divider(c)
			hintHeading(c, "GLOBS AND GIT")
			hintRow(c, "src/**/*.{c,h} !test/ init", "include and exclude globs")
			hintRow(c, "git:modified", "also: staged untracked clean tracked ignored conflict")

			ui.Divider(c)
			hintHeading(c, "CONTENT SEARCH")
			ui.Text(c, "Content mode is smart-case, and regexes match within a line, as in ripgrep. Text files up to 1 MB (10 MB in git repos) are indexed, minus gitignored paths and heavy assets. Naming a file still greps it directly.").TextColor(t.TextMuted).MaxWidth(760)
			ui.Text(c, "To skip more folders, list one path per line in ~/Library/Application Support/FSearch/content-skip.txt (~ and # comments work).").TextColor(t.TextMuted).MaxWidth(760)

			ui.Divider(c)
			hintHeading(c, "RESULTS")
			ui.Text(c, "Double-click or press Enter to open. Click a column header to sort. Cmd/Shift-click selects several rows; right-click opens, reveals in Finder or copies their paths.").TextColor(t.TextMuted).MaxWidth(760)

			ui.Divider(c)
			hintHeading(c, "FIRST RUN")
			ui.Text(c, "The daemon indexes the whole disk on first launch (about a minute, up to ~1 GB of memory). Searches show the indexing message until it is ready, then retry by themselves. Grant Full Disk Access to the bundled fsearch binary to include protected folders.").TextColor(t.TextMuted).MaxWidth(760)
		})
	})
}

func hintHeading(c *ui.Context, title string) ui.Element {
	return ui.Text(c, title).FontSize(11).Bold().TextColor(c.Theme().TextMuted)
}

// hintRow lines up an example query with what it does, without using a
// mono-spaced font that would look out of place in the native UI. The example
// column grows just enough for longer queries such as the README's filters.
func hintRow(c *ui.Context, example, meaning string) ui.Element {
	w := 190 + len(example)*6
	if w < 210 {
		w = 210
	}
	if w > 360 {
		w = 360
	}
	return ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, example).Width(float32(w)).Bold().SingleLine()
		ui.Text(c, meaning).TextColor(c.Theme().TextMuted)
	})
}

func (a *app) statusBar(c *ui.Context) ui.Element {
	t := c.Theme()
	return ui.Row(c).Padding(6, 10).Gap(10).Children(func() {
		switch {
		case a.busy:
			ui.Spinner(c)
			ui.Text(c, "Searching…").TextColor(t.TextMuted)
		case a.err != "":
			ui.Text(c, a.err).TextColor(t.Danger).SingleLine()
		case strings.TrimSpace(a.query) != "":
			unit := "files"
			if a.mode == modeContent {
				unit = "matches"
			}
			msg := fmt.Sprintf("%d %s", len(a.rows), unit)
			if len(a.rows) >= resultLimit {
				msg = fmt.Sprintf("first %d %s", resultLimit, unit)
			}
			ui.Text(c, msg+" · "+formatDuration(a.took)).TextColor(t.TextMuted)
		}
		ui.Spacer(c)
		if a.hasSt {
			if a.status.ContentPending > 0 {
				ui.Badge(c, fmt.Sprintf("indexing content: %d left", a.status.ContentPending))
			}
			ui.Text(c, fmt.Sprintf("%s files indexed", formatCount(a.status.Entries))).TextColor(t.TextMuted)
		}
	})
}

// startSearch runs the current query on a goroutine. Answers of searches the
// user has typed past are dropped.
func (a *app) startSearch() {
	q := strings.TrimSpace(a.query)
	if q == "" {
		a.clear()
		return
	}
	a.searchID++
	id, mode := a.searchID, a.mode
	a.busy, a.err, a.notice = true, "", ""

	go func() {
		start := time.Now()
		var rows []row
		var err error
		// took is the time fsearch says it spent, which is what the user
		// wants to see; the round trip is the fallback.
		var took time.Duration
		if mode == modeContent {
			var res GrepResult
			if res, err = a.searcher.Grep(q, resultLimit); err == nil {
				rows = grepRows(res, resultLimit)
				took = time.Duration(res.TookUS) * time.Microsecond
			}
		} else {
			var res NameResult
			if res, err = a.searcher.Search(q, resultLimit); err == nil {
				rows = nameRows(res)
				took = time.Duration(res.TookUS) * time.Microsecond
			}
		}
		if took == 0 {
			took = time.Since(start)
		}
		a.apply(func() {
			if id != a.searchID {
				return
			}
			a.busy = false
			a.took = took
			a.selected = -1
			a.chosen.Clear()
			if err != nil {
				a.rows, a.sorted = nil, nil
				var ie *IndexingError
				if asIndexing(err, &ie) {
					a.notice = ie.Message
					a.watchIndexing()
				} else {
					a.err = err.Error()
				}
				return
			}
			a.rows = rows
			a.resort()
		})
		a.refreshStatus()
	}()
}

// watchIndexing searches again once the first index is built.
func (a *app) watchIndexing() {
	go func() {
		for {
			time.Sleep(time.Second)
			st, err := a.searcher.Status()
			if err != nil {
				var ie *IndexingError
				if asIndexing(err, &ie) {
					a.apply(func() { a.notice = ie.Message })
					continue
				}
				return
			}
			a.apply(func() {
				a.status, a.hasSt = st, true
				if a.notice != "" {
					a.notice = ""
					a.pending = time.Now().Add(-debounce)
				}
			})
			return
		}
	}()
}

func (a *app) refreshStatus() {
	st, err := a.searcher.Status()
	if err != nil {
		return
	}
	a.apply(func() { a.status, a.hasSt = st, true })
}

// apply changes the state on the main thread. Without a window, as in tests,
// the change waits in a queue that the next frame drains, which is the same
// thread discipline win.Update gives.
func (a *app) apply(fn func()) {
	if a.win != nil {
		a.win.Update(fn)
		return
	}
	a.mu.Lock()
	a.queued = append(a.queued, fn)
	a.mu.Unlock()
}

func (a *app) drain() {
	a.mu.Lock()
	q := a.queued
	a.queued = nil
	a.mu.Unlock()
	for _, fn := range q {
		fn()
	}
}

// idle reports whether no search is waiting, running or unapplied.
func (a *app) idle() bool {
	a.mu.Lock()
	n := len(a.queued)
	a.mu.Unlock()
	return n == 0 && a.pending.IsZero() && !a.busy
}

func (a *app) sortChanged() bool { return true }

// resort orders the rows as the table's sort says; with none, in the order
// fsearch ranked them.
func (a *app) resort() {
	a.sorted = append(a.sorted[:0], a.rows...)
	if a.sort.Column == "" {
		return
	}
	key := func(r row) (string, int64) {
		switch a.sort.Column {
		case "name":
			return strings.ToLower(r.Name), 0
		case "dir":
			return strings.ToLower(r.Dir), 0
		case "size":
			return "", r.Size
		case "mtime":
			return "", r.MTime
		case "line":
			return "", int64(r.Line)
		}
		return "", 0
	}
	sort.SliceStable(a.sorted, func(i, j int) bool {
		si, ni := key(a.sorted[i])
		sj, nj := key(a.sorted[j])
		less := si < sj || (si == sj && ni < nj)
		if a.sort.Descending {
			return si > sj || (si == sj && ni > nj)
		}
		return less
	})
}

func (a *app) path(i int) (string, bool) {
	if i < 0 || i >= len(a.sorted) {
		return "", false
	}
	return a.sorted[i].Path, true
}

func (a *app) open(p string) {
	if err := mygo.Shell.OpenPath(p); err != nil {
		a.err = err.Error()
	}
}

func (a *app) reveal(p string) {
	mygo.Shell.ShowItemInFolder(p)
}

func nameRows(res NameResult) []row {
	rows := make([]row, 0, len(res.Hits))
	for i, h := range res.Hits {
		rows = append(rows, row{
			Key:  fmt.Sprintf("n%d:%s", i, h.Path),
			Path: h.Path, Dir: filepath.Dir(h.Path), Name: filepath.Base(h.Path),
			Kind: h.Kind, Size: h.Size, MTime: h.MTime,
		})
	}
	return rows
}

func grepRows(res GrepResult, limit int) []row {
	var rows []row
	for _, f := range res.Files {
		for _, m := range f.Matches {
			rows = append(rows, row{
				Key:  fmt.Sprintf("g%d:%s", len(rows), f.Path),
				Path: f.Path, Dir: filepath.Dir(f.Path), Name: filepath.Base(f.Path),
				Kind: "file", Line: m.Line, Text: m.Text,
			})
			if len(rows) >= limit {
				return rows
			}
		}
	}
	return rows
}

func main() {
	a := newApp(NewClient())
	// Keep the process alive when the window is hidden; a menu-bar app quits
	// only on Quit. On non-macOS platforms there is no menu bar to click, so
	// close there still exits.
	mygo.App.OnWindowAllClosed(func() {
		if runtime.GOOS != "darwin" {
			mygo.App.Quit()
		}
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { quitting = true })
	// Re-launching the app or clicking its Dock icon should bring a hidden
	// window back instead of leaving the process running with nothing to see.
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows && a.win != nil && !a.win.IsDestroyed() {
			showWindow(a.win)
		}
	})
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "FSearch",
			Width:     980,
			Height:    640,
			MinWidth:  560,
			MinHeight: 360,
			StateKey:  "main",
			Content:   ui.View(a.view),
		})
		setupShell(a.win)
		go a.refreshStatus()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
