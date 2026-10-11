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

Not-found rows stay hidden unless `-a`. `--json` prints a JSON array; each row includes `category`.

On a terminal, a gray progress line stays at the bottom while sites answer (`checking 281 sites · 41 left`). Found hits then print grouped by category, found results first: Social, Gaming, Dev, Music, Creator, Other. Hits use a green mark and a bold name, URLs are faint, misses are gray, and errors are yellow.

When stdout is not a terminal, blint prints found profile URLs, one per line, with no color and no progress line. Ctrl-C stops in-flight requests and exits.

## Site list

`sites.json` is embedded with `go:embed`. It is the detection list from [155xp/osint](https://github.com/155xp/osint) `data/sites.json`: category, check URL, profile URL, status code, not-found text, and redirect markers. A 200 is a miss when the missing text matches. A 3xx whose location matches `m_redirect` is a miss. The list is validated public checks from Sherlock and Maigret (MIT) and Project WhatsMyName (CC BY-SA 4.0).

To resync:

1. Copy `data/sites.json` from 155xp/osint main over `sites.json`.
2. Keep `category`, `uri_check`, `uri_pretty`, `e_code`, `e_string`, `m_code`, `m_string`, `m_redirect`, and any `m_alt_code` / `m_alt_string`. Enrichment fields are unused.
3. Rebuild. The next `go install` embeds the updated file.

Checks use public HTTPS endpoints only. No API key is read from the environment.
