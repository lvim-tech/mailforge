# mailforge

A terminal configurator for [NeoMutt](https://neomutt.org). It owns the parts of a mail setup that
are tedious to keep consistent — accounts, key bindings, sync, the sidebar counters — and writes
them out as plain configuration files you can read.

It does not replace NeoMutt, `mbsync` or `pass`. It configures them, and stays out of the files
you write yourself.

## What it manages

- **Accounts** — IMAP and SMTP, discovered rather than typed. Given an address it reads the
  provider's autoconfig (Mozilla ISPDB), falls back to MX records and to the common host names,
  and offers what it found. Passwords go into `pass`; nothing is stored in mailforge's own config.
- **Keys** — every NeoMutt function, per menu, with its stock binding shown beside yours. Rebind
  what you use, leave the rest alone, and the file is regenerated with only the lines that differ
  from a default install.
- **Sync** — the `mbsync` channels for each account, and a systemd user timer that runs them.
- **Sidebar** — the mailbox list, the folder hooks, and the three NeoMutt timers behind
  "when do the unread counts catch up".
- **Doctor** — a checklist that verifies what exists, what is reachable, and what is missing,
  with the reason it matters rather than a red cross.

## Requirements

- `neomutt`
- `isync` (for `mbsync`)
- `pass`, with a GPG key already set up
- `msmtp` is not needed — NeoMutt sends directly over SMTP

## Install

```sh
go install github.com/lvim-tech/mailforge@latest
```

Or from a checkout:

```sh
git clone https://github.com/lvim-tech/mailforge
cd mailforge
go build -o ~/bin/mailforge .
```

## Use

```sh
mailforge                       # the interface: Accounts, Keys, Settings, Doctor
mailforge fetch                 # run mbsync for every account
mailforge apply                 # re-render every account's files from the registry
mailforge unread                # total unread, for a status bar
mailforge unread --per-account  # one line per address
mailforge unread --json         # one Waybar custom-module object
mailforge unread --watch        # stream the total, printing only when it changes
```

`apply` touches no passwords: it reads none, asks for none and writes none. It is for when the
generated files have gone stale without any account changing — a moved pass entry, an edited
template — which otherwise meant walking each account through a wizard that verifies against the
real servers to rewrite a text file.

`--json` is what a status bar reads, so the bar needs no wrapper script of its own. Which accounts
exist, where their maildirs are and what each server calls its spam folder are all questions the
registry answers; a script in the bar's configuration would carry a copy of all three and be wrong
the first time one of them moved.

`unread` counts both halves of each maildir — everything in `new/`, plus anything in `cur/` whose
filename lacks the `S` flag — for INBOX and the account's spam folder. A message a client has
looked at without opening still counts, and junk that a filter got wrong is still unread mail.

## The files it writes

Everything mailforge generates carries a header saying so, and is rewritten on every apply:

| File | What it holds |
| --- | --- |
| `~/.config/neomutt/mailforge/accounts.rc` | mailbox list, sidebar headers, folder hooks |
| `~/.config/neomutt/mailforge/accounts/<address>.rc` | one account's IMAP, SMTP, identity and signature |
| `~/.config/neomutt/mailforge/keys.rc` | the key bindings that differ from a default install |
| `~/.config/neomutt/mailforge/options.rc` | editor, the three timers, `mail_check_stats` |
| `~/.mbsyncrc` | the mbsync channels, **between markers only** |
| `~/.config/systemd/user/mailforge-mbsync.{service,timer}` | the fetch timer |

Files it seeds **once** and never touches again — they are yours from the moment they exist:
`settings.rc`, `colors.rc`, `bindings.rc`, `mailcap`.

`~/.mbsyncrc` is the only foreign file it edits, and it edits only between its own markers. Nothing
outside them is read, moved or rewritten.

## Configuration

Settings live in `~/.config/mailforge/config.json` and are edited in the Settings tab. The full set,
at its defaults:

```json
{
  "maildir_root": "~/.local/share/mail",
  "neomutt_dir": "~/.config/neomutt",
  "mbsync_rc": "~/.mbsyncrc",
  "pass_prefix": "mail",
  "cert_file": "/etc/ssl/ca-bundle.pem",
  "real_name": "",
  "editor": "vim",
  "sidebar_counts": true,
  "sidebar_counts_every": 5,
  "check_every_seconds": 10,
  "wake_every_seconds": 5,
  "sync_every_seconds": 300,
  "fetch_when_logged_out": false,
  "sync": {
    "create": "Both",
    "expunge": "Both",
    "max_messages": 0,
    "expire_unread": false
  },
  "verify_timeout_sec": 10,
  "keys": null,
  "ispdb": true
}
```

`keys` holds the bindings that differ from a default install, written by the Keys tab. The paths
above are shown with `~` for brevity; they are stored expanded.

`editor` is a setting rather than a reference to `$EDITOR` on purpose: NeoMutt expands
`set editor` once, when the config is parsed, out of whatever environment started it. Launched from
a desktop entry that exports nothing, the value comes out empty and composing a message fails.

### The three timers

They are separate because NeoMutt nests them, and one number would answer the wrong question:

- `wake_every_seconds` (`timeout`) — how long NeoMutt waits for a key before looking around at all.
- `check_every_seconds` (`mail_check`) — the minimum gap between checks for new mail, inside that look.
- `sidebar_counts_every` (`mail_check_stats_interval`) — the minimum gap between recounts of the
  unread totals, inside that check.

Each is a ceiling over the next: a recount is real work on a large maildir, a new-mail check is
not, and neither should be forced to the pace of the keyboard poll.

### Fetching on a timer

`sync_every_seconds` drives a systemd **user** timer. A user timer runs only while you are logged in unless
lingering is enabled, and mailforge can turn that on from the Settings tab.

The units are named `mailforge-mbsync`, not `mbsync`, so a hand-written timer of your own keeps its
name and its contents.

## Licence

BSD 3-Clause. See [LICENSE](LICENSE).
