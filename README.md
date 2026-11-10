# Tidy It

A read-only report of stale things on a Linux laptop that cleaners like
BleachBit miss: unused Docker images and volumes, old snap revisions, apt
leftovers, stale network connections, failed units, old downloads, large
caches and more. Tidy It never changes anything; it shows what it found, how
sure it is that each item is safe to remove, and the command to remove it.

It replaces the old `laptop-report` bash script (see git history).

## Use

    tidyit                # compact overview, biggest first
    tidyit --full         # every item with its command
    tidyit -v             # everything, plus timings and plugin debug output
    tidyit --check docker # only some checks (ids, globs or plugin ids)
    tidyit --json         # machine-readable report
    tidyit --html r.html  # self-contained HTML report to keep or share
    tidyit checks -v      # list checks and their options
    tidyit version        # print the version

Settings go in `~/.config/tidyit/config.toml` (overrides only; see
`internal/config/config.go` for the format).

## Install from this checkout

    eval "$(mise env -s bash)"
    go build -o bin/tidyit ./cmd/tidyit
    ln -s "$PWD/bin/tidyit" ~/.local/bin/tidyit
    ln -s ~/.local/bin/tidyit ~/.local/bin/laptop-report   # old name, optional
    mkdir -p ~/.local/share/tidyit/plugins
    ln -s "$PWD/plugins/home-usage" ~/.local/share/tidyit/plugins/

Rebuild with the `go build` line after pulling changes.

## Desktop app

`tidyit-gui` is the same report as a GTK4/libadwaita window. A sidebar
shows disk usage and the flagged checks, grouped into safe to remove,
worth reviewing and your call. Picking one opens its items, each with a
copy button for its command. Below 600 px wide the sidebar and the page
take turns. Like the CLI it never runs anything. It needs the GTK
headers to build; the first build compiles the GTK bindings and takes
around ten minutes, later ones a few seconds.

    sudo apt install libgtk-4-dev libadwaita-1-dev libgirepository1.0-dev
    eval "$(mise env -s bash)"
    go build -o bin/tidyit-gui ./cmd/tidyit-gui
    ln -s "$PWD/bin/tidyit-gui" ~/.local/bin/tidyit-gui
    cp data/com.inulty.TidyIt.desktop ~/.local/share/applications/

The bindings are pinned to gotk4-adwaita's `adw-1.5` branch to match the
libadwaita in Ubuntu 24.04; newer branches need newer headers.

## Plugins

External plugins are folders with a `plugin.toml` and an executable `run`,
in any language. See `plugins/home-usage` for an example and
`internal/external/external.go` for the protocol.
