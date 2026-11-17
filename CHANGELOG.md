# Changelog

## Unreleased

- Plugin manifests can give options a `title`, a `unit` and a `format`
  (`path` or `glob`), and give the plugin a `category` (`files`,
  `packages` or `system`). All are optional; protocol stays 1. The
  coming settings dialog uses them, and `tidyit checks -v` shows units.
- Tidy It can write its config file: settings changes rewrite
  `config.toml` with a "Managed by Tidy It" header. A hand-written file
  is first copied to `config.toml.bak`, and a symlinked config is
  written through the link.

## v0.1.0-beta.1 — 2026-11-10

First beta. Tidy It is read-only: it shows what it found and the command
to remove each item, and never runs anything.

- `tidyit`: terminal report with a compact overview, `--full` and `-v`
  detail, `--json` and a self-contained `--html` report. `tidyit checks`
  lists every check and its options; `tidyit version` prints the version.
- `tidyit-gui`: a GTK4/libadwaita window with disk usage and the flagged
  checks grouped by how safe they are to remove. Each check opens a page
  with its items, Show in Files and copy buttons. It adapts to narrow
  windows.
- Checks: disk usage, trash, old downloads, large caches, old temporary
  files, app data unused for a year, very large files, broken login items,
  Docker (unused images, volumes and networks), snap (old revisions,
  unused runtimes, apps never opened), Flatpak unused runtimes, apt
  (unneeded packages, leftover configs), stale NetworkManager
  connections, failed systemd units and journal size. BleachBit is
  available but off by default.
- External plugins in any language (protocol 1), with `home-usage`
  bundled as an example. Plugins are not yet pinned by hash.
- Reports record the version that produced them (`generator`).
- Released as a `.deb` and a tarball. Plugins installed system-wide
  under `/usr/share/tidyit/plugins` are found, and a user's copy of a
  plugin overrides an installed one.
