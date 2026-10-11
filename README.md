# blint

Check a username against public profile pages.

```
go install github.com/155xp/blint@latest
```

```
blint <username>
blint a,b,c
blint -a octocat
blint --json octocat
```

Found profiles print as each site answers. Not-found rows stay hidden unless `-a`. `--json` prints a JSON array with counts and the same rows.

On a terminal, one progress line stays at the bottom:

```
checking 80 sites · 41 left
```

When stdout is not a terminal, blint prints found profile URLs, one per line, with no color and no progress line. Ctrl-C stops in-flight requests and exits.

## Site list

`sites.json` is embedded with `go:embed`. It is the detection list from [155xp/osint](https://github.com/155xp/osint) `src/data/sites.json`: check URL, profile URL, status code, and the not-found text or redirect marker. A 200 is a miss when that marker matches. Strings and URL patterns come from [Project WhatsMyName](https://github.com/WebBreacher/WhatsMyName) (CC BY-SA 4.0).

To resync:

1. Copy `src/data/sites.json` from 155xp/osint over `sites.json`.
2. Keep `uri_check`, `uri_pretty`, `e_code`, `e_string`, `m_code`, `m_string`, and any `m_alt_code` / `m_alt_string`. Enrichment fields are unused.
3. Rebuild. The next `go install` embeds the updated file.

Checks use public HTTPS endpoints only. No API key is read from the environment.
