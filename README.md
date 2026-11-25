# FSearch GUI

A native desktop front end for [fsearch](https://github.com/noahdunnagan/fsearch),
the whole-disk file search for macOS, written in Go with
[MyGo](https://mygo.egoist.dev/) native UI (no webview).

- **Name** mode: fuzzy, typo-tolerant file names as you type.
- **Content** mode: search inside files through fsearch's trigram index.
- Sortable, resizable table. Double-click or Enter opens a file; right-click for
  *Open*, *Reveal in Finder* and *Copy Path*. Cmd/Shift-click chooses several
  rows, which those actions apply to together.
- The status bar shows fsearch's own search time, the index size, and progress
  while content is still being indexed.

It talks to the fsearch daemon over its Unix socket
(`~/Library/Application Support/FSearch/fsearch.sock`, JSON lines). If no daemon
is running it starts `fsearch serve` in its own session, so the daemon keeps the
index fresh after the app closes.

## Requirements

- macOS (fsearch is macOS only).
- Go 1.27.3 or later (MyGo v0.9.0 requires it). With `GOTOOLCHAIN=auto`, the
  default, `go` downloads it for you.
- The `fsearch` binary. The packaged `.app` bundles it in
  `Contents/Resources`; for `mygo dev` and tests it is looked for at
  `resources/fsearch` (gitignored — build it yourself), then on `PATH`, at
  `~/.local/bin/fsearch`, or at `$FSEARCH_BIN`:

  ```sh
  git clone https://github.com/noahdunnagan/fsearch
  cd fsearch && cargo build --release
  cp target/release/fsearch /path/to/Fsearch-Mygo-GUI/resources/
  ```

## Run

```sh
./build-universal.sh  # test + build arm64 and universal (Intel + Apple Silicon) apps/DMGs
go tool mygo dev      # live reload
go tool mygo build    # build/darwin-arm64/*.app and a .dmg
go test -race ./...   # view tests, no window and no daemon needed
```

`build-universal.sh` uses the locally installed Go (`GOTOOLCHAIN=local`; it
never downloads a toolchain), merges and signs `resources/fsearch` from the
per-architecture binaries, and sets `MYGO_LDFLAGS=-macos=12.0.0` so the app also
launches on macOS 12. The app's empty window shows the same query cheat sheet
as the examples below.

## Things to know

- **The first run indexes the whole disk**, whatever `$HOME` is: about a minute
  and up to ~1 GB of memory (see fsearch's README). Until it finishes, searches
  show fsearch's "indexing" message, and the app searches again by itself when
  the index is ready. Content search becomes available after the names.
- **Full Disk Access**: without it fsearch skips protected folders. Grant it to
  the `fsearch` binary (not this app), as fsearch's README describes.
- **Close hides, Quit exits**: closing the window hides it to the menu bar and
  removes the Dock icon; ⌘⇧Space (or the menu-bar icon) shows it again and
  restores the Dock icon. The fsearch daemon keeps indexing while hidden.
- **Query syntax** is fsearch's. Examples:
  `fsearch 'readme in:~/Developer'`,
  `fsearch 'type:image size:>5mb mtime:<7d'`,
  `fsearch 'ext:rs regex:fn\s+\w+_dir'`,
  `fsearch 'sym:apply_dir'`,
  `fsearch 'src/**/*.{c,h} !test/ init'`,
  `fsearch 'git:modified in:~/Developer/app'`.
  Words are fuzzy anywhere in a name (two typos in names of 8+ letters);
  several words can spell one name, and accented names match however they were
  stored. Also `'exact`, `^prefix`, `suffix$` and `!exclude`. Filters: `ext:`,
  `type:`, `kind:`, `in:`, `size:`, `mtime:`, `re:`, `path:`, `grep:`, `regex:`,
  `sym:`, `limit:` and `git:` (`modified staged untracked clean tracked ignored
  conflict`). In Content mode, non-filter words are the text searched for;
  matching is smart-case and regexes match within a line, as in ripgrep.
- `TestLiveDaemon` runs against a real daemon and is opt-in
  (`FSEARCH_LIVE=1 go test -run TestLiveDaemon -v`) because of the first-run
  crawl above.
- `SNAPSHOT_DIR=/tmp/shots go test -run TestSnapshot` writes PNGs of the window.

## Layout

| File | |
| --- | --- |
| `main.go` | state, view, search flow (debounce, stale answers dropped) |
| `shell.go` | global shortcut, menu-bar icon, close-to-tray and Dock icon |
| `client.go` | fsearch socket client, daemon start-up |
| `format.go` | sizes, dates, counts |
| `main_test.go` | view tests with a fake searcher |
| `build-universal.sh` | one-shot arm64 + universal build and test |
| `resources/merge-fsearch.sh` | merge and ad-hoc sign the bundled fsearch |
