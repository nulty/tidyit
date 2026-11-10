# home-usage

An example tidy plugin, written in bash. It lists the largest top-level
folders in your home directory. It is informational and never flags
anything. It replaces the "Largest folders in home" section of
`laptop-report`.

Install it by linking the folder into tidy's plugin directory:

    mkdir -p ~/.local/share/tidy/plugins
    ln -s "$PWD" ~/.local/share/tidy/plugins/home-usage

Try it without installing:

    TIDY_PLUGIN_PATH=$PWD/.. tidy --check home-usage

Options (in `~/.config/tidy/config.toml`):

    [checks.home-usage.largest-folders]
    count = 5

## The protocol, by example

tidy runs `./run` in this folder and writes one line to its stdin:

    {"protocol":1,"check":"largest-folders","instance":"home-usage.largest-folders","options":{"count":10}}

The plugin answers with JSON lines on stdout: `item`, `note`, `summary` or
`skip` messages. See `internal/external/external.go` in the tidy source for
the full list of fields. A non-zero exit is reported as an error, and
stderr is shown with `tidy -v`.
