package main

import (
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// css adds the few styles libadwaita has no class for. Colours come from
// libadwaita's named colours, so light, dark and the accent all follow
// the system.
const css = `
.disk-card {
	background-color: alpha(@window_fg_color, 0.06);
	border-radius: 12px;
	margin: 2px 0 4px 0;
}
.disk-card:selected { background-color: alpha(@window_fg_color, 0.1); }
levelbar.disk block.filled { background-color: @accent_bg_color; }
levelbar.disk.full block.filled { background-color: @warning_bg_color; }
levelbar.disk block.empty { background-color: alpha(@window_fg_color, 0.1); }
levelbar.disk trough { padding: 0; }
levelbar.disk.thin block { min-height: 6px; border-radius: 3px; }
levelbar.disk.thick block { min-height: 10px; border-radius: 5px; }
.sidebar-heading {
	font-size: 0.9em;
	font-weight: 700;
	padding: 12px 8px 4px 8px;
}
.sidebar-rule-row, .sidebar-rule-row:hover { background: none; min-height: 0; padding: 8px 8px 4px 8px; }
.sidebar-rule { min-height: 1px; background-color: alpha(@window_fg_color, 0.1); }
.round-button { border-radius: 999px; padding: 6px 16px; }
.badge {
	padding: 2px 9px;
	border-radius: 999px;
	font-size: 0.85em;
	font-weight: 600;
	background-color: alpha(@window_fg_color, 0.07);
}
.icon-tile {
	background-color: alpha(@window_fg_color, 0.07);
	border-radius: 12px;
	min-width: 44px;
	min-height: 44px;
}
.hint-banner {
	background-color: alpha(@accent_bg_color, 0.16);
	border-radius: 12px;
	padding: 12px 14px;
}
.tile { padding: 16px 18px; }
.overview-card { padding: 20px 22px; }
.big-number { font-size: 2em; font-weight: 700; }
`

func loadCSS() {
	p := gtk.NewCSSProvider()
	p.LoadFromString(css)
	gtk.StyleContextAddProviderForDisplay(gdk.DisplayGetDefault(), p, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
}
