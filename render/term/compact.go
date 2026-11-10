package term

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"tidy/internal/fsx"
	"tidy/pkg/report"
)

const (
	previewItems = 3  // items named on a compact row
	maxTitle     = 34 // title column width cap
)

// renderCompact writes one line per flagged check, biggest first, then the
// checks that found nothing or did not run.
func renderCompact(w io.Writer, rep *report.Report, groups []Group, opt Options) {
	c := newPalette(opt.Color)
	headline := map[string]bool{}
	for _, id := range opt.Headline {
		headline[id] = true
	}

	var all, rows, oks, skipped, errs []report.Result
	var heads []string
	for _, g := range groups {
		for _, r := range g.Results {
			all = append(all, r)
			switch {
			case headline[r.ID] && r.Status == report.StatusFlag:
				for _, it := range r.Items {
					heads = append(heads, strings.TrimSpace(it.Label+" "+it.Detail))
				}
			case headline[r.ID] && r.Status == report.StatusOK:
				// fine, nothing to say
			case r.Status == report.StatusFlag:
				rows = append(rows, r)
			case r.Status == report.StatusOK:
				oks = append(oks, r)
			case r.Status == report.StatusSkipped:
				skipped = append(skipped, r)
			case r.Status == report.StatusError:
				errs = append(errs, r)
			}
		}
	}
	// Biggest first; checks with no sizes keep their report order after them.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].TotalSize() > rows[j].TotalSize() })

	fmt.Fprintf(w, "%sTidy report%s  %s%s · %s · %s%s\n", c.bold, c.rst, c.dim,
		rep.StartedAt.Format("2006-01-02 15:04"), rep.Host, rep.FinishedAt.Sub(rep.StartedAt).Round(100_000_000), c.rst)
	var line []string
	for _, h := range heads {
		line = append(line, c.yel+h+c.rst)
	}
	if n := report.Total(all, report.ConfidenceSafe); n > 0 {
		line = append(line, c.grn+fsx.Human(n)+" safe to remove"+c.rst)
	}
	if n := report.Total(all, report.ConfidenceReview); n > 0 {
		line = append(line, fsx.Human(n)+" to review")
	}
	if len(rows) == 0 && len(errs) == 0 {
		line = append(line, c.grn+"nothing flagged"+c.rst)
	}
	fmt.Fprintf(w, "%s\n\n", strings.Join(line, "  ·  "))

	titleW := 0
	for _, r := range append(append([]report.Result{}, rows...), errs...) {
		titleW = max(titleW, utf8.RuneCountInString(r.Title))
	}
	titleW = min(titleW, maxTitle)

	for _, r := range rows {
		conf := r.Caution()
		size := ""
		if n := r.TotalSize(); n > 0 {
			size = fsx.Human(n)
		}
		// "  review  7.2G  Title....  15  "
		lead := fmt.Sprintf("  %-6s  %5s  %s  %3d  ", conf, size, pad(r.Title, titleW), len(r.Items))
		preview := itemPreview(r.Items, room(opt.Width, lead))
		fmt.Fprintf(w, "  %s%-6s%s  %5s  %s  %3d  %s%s%s\n", confColor(conf, c), conf, c.rst, size,
			pad(r.Title, titleW), len(r.Items), c.dim, preview, c.rst)
	}
	for _, r := range errs {
		lead := fmt.Sprintf("  %-6s  %5s  %s  %3s  ", "error", "", pad(r.Title, titleW), "")
		fmt.Fprintf(w, "  %serror %s  %5s  %s  %3s  %s\n", c.red, c.rst, "", pad(r.Title, titleW), "",
			clip(r.Error, room(opt.Width, lead)))
	}

	if !opt.OnlyFlagged {
		if len(rows)+len(errs) > 0 && len(oks)+len(skipped) > 0 {
			fmt.Fprintln(w)
		}
		if len(oks) > 0 {
			names := make([]string, len(oks))
			for i, r := range oks {
				names[i] = r.Title
			}
			fmt.Fprint(w, wrapList(c.grn+"  ok    "+c.rst, "        ", names, opt.Width))
		}
		for _, r := range skipped {
			lead := "  --    " + r.Title + ": "
			fmt.Fprintf(w, "%s  --    %s: %s%s\n", c.dim, r.Title, clip(r.SkipReason, room(opt.Width, lead)), c.rst)
		}
	}
	fmt.Fprintf(w, "\n%sdetails: tidy --full    one check: tidy --full --check <id>%s\n", c.dim, c.rst)
}

// itemPreview names the first few items, with sizes, fitting width runes
// (0 = no limit), and says how many more there are.
func itemPreview(items []report.Item, width int) string {
	var parts []string
	for i, it := range items {
		if i == previewItems {
			break
		}
		p := it.Label
		if it.Size > 0 {
			p += " " + fsx.Human(it.Size)
		}
		parts = append(parts, p)
	}
	for n := len(parts); n >= 1; n-- {
		more := ""
		if rest := len(items) - n; rest > 0 {
			more = fmt.Sprintf(", +%d", rest)
		}
		s := strings.Join(parts[:n], ", ")
		if width <= 0 || utf8.RuneCountInString(s+more) <= width {
			return s + more
		}
		if n == 1 {
			// Shorten the one name but keep the count visible.
			return clip(s, max(width-utf8.RuneCountInString(more), 4)) + more
		}
	}
	return ""
}

// room is what is left of the terminal width after lead; 0 = no limit.
func room(width int, lead string) int {
	if width <= 0 {
		return 0
	}
	return max(width-utf8.RuneCountInString(lead), 10)
}

// clip shortens s to width runes with an ellipsis; width 0 = no limit.
func clip(s string, width int) string {
	if width <= 0 || utf8.RuneCountInString(s) <= width {
		return s
	}
	r := []rune(s)
	return string(r[:width-1]) + "…"
}

func pad(s string, width int) string {
	s = clip(s, width)
	return s + strings.Repeat(" ", max(0, width-utf8.RuneCountInString(s)))
}

// wrapList joins names with " · ", wrapping at width under indent.
func wrapList(first, indent string, names []string, width int) string {
	var b strings.Builder
	b.WriteString(first)
	col := utf8.RuneCountInString(stripANSI(first))
	for i, n := range names {
		sep := ""
		if i > 0 {
			sep = " · "
		}
		if width > 0 && i > 0 && col+len(sep)+utf8.RuneCountInString(n) > width {
			b.WriteString("\n" + indent)
			col = utf8.RuneCountInString(indent)
			sep = ""
		}
		b.WriteString(sep + n)
		col += utf8.RuneCountInString(sep + n)
	}
	b.WriteString("\n")
	return b.String()
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\033' {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func confColor(conf report.Confidence, c palette) string {
	switch conf {
	case report.ConfidenceSafe:
		return c.grn
	case report.ConfidenceReview:
		return c.yel
	}
	return c.dim
}
