# gh-pr-review — review-comment & suggestion templates

Patterns for writing effective, kind, specific PR feedback. Every
template below is the literal `body:` string you'd paste into a review
payload (see [`commands.md`](commands.md) section 6). Customise the
severity label, file path, and one-sentence fix — keep everything else
crisp.

## Severity tag glossary

Prefix every line comment with one of these tags so the author can
triage at a glance (match the [team PR policy](../../teams/example-team/pr-review.md)):

| Tag           | When to use                                                                         |
| ------------- | ----------------------------------------------------------------------------------- |
| **Blocker**   | Security, data loss, correctness regression, missing tests for public behaviour.     |
| **Must-fix**  | Maintainability, perf regression on a hot path, unclear error handling.              |
| **Nit**       | Style, naming, comments. Author may ignore without reply.                            |
| **Question**  | Genuine clarification — NOT disguised criticism.                                     |
| **Praise**    | Yes, really. Call out a clever or careful change when you see one.                   |

---

## 1. Inline single-line suggestion

GitHub renders a ```` ```suggestion ``` ```` block as an "Apply
suggestion" button. The block must contain the **replacement for the
single `line`** the comment targets — nothing more, nothing less.

```text
Must-fix — guard against a nil `cfg`:

```suggestion
	if cfg == nil {
		return nil, errors.New("orders: nil config")
	}
```
```

Payload form (inside `comments[]`):

```json
{
  "path": "internal/orders/handler.go",
  "line": 88,
  "side": "RIGHT",
  "body": "Must-fix — guard against a nil `cfg`:\n\n```suggestion\n\tif cfg == nil {\n\t\treturn nil, errors.New(\"orders: nil config\")\n\t}\n```"
}
```

Tips:

- Use the file's real indentation (tabs vs. spaces) — the button
  applies the block verbatim.
- End the body with the closing fence; do **not** add a trailing
  explanation after the ```` ``` ````.

---

## 2. Multi-line suggestion

To replace a range, set `start_line` and `start_side` and put the full
replacement inside the `suggestion` block:

```json
{
  "path": "internal/orders/handler.go",
  "start_line": 64,
  "start_side": "RIGHT",
  "line": 68,
  "side": "RIGHT",
  "body": "Must-fix — fold the three `if err != nil` branches into a single early-return:\n\n```suggestion\n\tif err != nil {\n\t\treturn nil, fmt.Errorf(\"orders: fetch: %w\", err)\n\t}\n\torders, err := h.store.List(ctx, in)\n```"
}
```

Rule of thumb: the replacement should span the same number of lines
(or fewer — GitHub tolerates contraction) as `start_line..line`.

---

## 3. Rename suggestion (single identifier)

Renames are a great use of suggestions because they're unambiguous.

```text
Nit — `sz` is our convention for byte-count variables; for element
counts prefer `n`:

```suggestion
	n := len(items)
```
```

For a rename across many call-sites, **don't** paste dozens of
suggestion blocks. Instead, write one review comment pointing at the
definition with a top-level suggestion, and leave a PR-level comment
saying "applied here; author can mirror at callsites 1..N".

---

## 4. Addition suggestion (insert a line without replacing)

GitHub's suggestion syntax only replaces `line` (or the range). To
*insert* a line, include the existing line inside the block too:

```text
Must-fix — add a context check before the long-poll:

```suggestion
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resp, err := client.Poll(ctx, req)
```
```

Target the line of the `resp, err := client.Poll(...)` call, not the
blank line above it.

---

## 5. Deletion suggestion

An empty ```` ```suggestion ``` ```` block removes the targeted line:

```text
Nit — leftover debug print:

```suggestion
```
```

Prefer this over a prose "please remove this line" — it's applied with
one click.

---

## 6. Blocker comment WITHOUT a suggestion

Sometimes there is no single-line fix — the author needs to rethink.
Don't force a suggestion block; a clear ask is better.

```text
Blocker — DELETE /orders/{id} has no authentication middleware.
Every other verb on this router goes through `auth.Required(...)`.

Either wrap the handler the same way or add a route-level middleware
in `router.go`. Please add a test that a request without a session
returns 401.

Linked: internal/auth/middleware.go:42, router.go:88.
```

---

## 7. Question vs. nit

Don't pass opinions off as questions. If it's an opinion, label it a
nit. Keep genuine questions genuine:

```text
Question — is the retry budget (`maxAttempts=5`) intentional here?
Our outbound budget elsewhere is 3; just checking before I approve.
```

vs.

```text
Nit — `maxAttempts=5` is higher than our default of 3. I'd drop it
unless there's a reason.
```

---

## 8. Review summary body templates

The review `body` (distinct from line comments) is where you post the
Ship/Hold verdict and link evidence.

### 8a. Approve

```markdown
## Verdict
Ship — clean diff, tests green, no Sonar regressions.

## Highlights
- `internal/orders/handler.go` now returns typed errors. 
- New integration test covers the 404 path.

## Evidence
- CI: https://github.com/.../actions/runs/123 (all green)
- Sonar (new code): OK

## Confidence
9/10 — skimmed migration but didn't run it locally.
```

### 8b. Request changes (blockers)

```markdown
## Verdict
Hold — 1 blocker + 2 must-fix. Please address before re-requesting review.

## Blockers
- `internal/orders/handler.go:88` — unauthenticated DELETE. See inline comment.

## Must-fix
- `db/migrations/0042_drop_legacy_col.sql:3` — drop without backfill.
- `internal/orders/handler.go:64-68` — stacked `if err != nil` — see inline suggestion.

## Nits
- `internal/orders/types.go:12` — spelling `Recieved` → `Received`.

## Evidence
- CI: https://github.com/.../actions/runs/123 — `e2e-smoke` fails (log: https://...).
- Sonar (new code): https://sonar.../dashboard?id=... — 1 CRITICAL.

## Confidence
8/10.
```

### 8c. Comment (neutral / questions)

```markdown
## Verdict
Comment — not blocking, but two questions before I approve.

## Questions
- `internal/orders/handler.go:88` — is DELETE behind a feature flag or live?
- `db/migrations/0042_drop_legacy_col.sql:3` — backfill plan?

## Nits
- None.

## Evidence
- CI: green
- Sonar: OK

## Confidence
7/10.
```

---

## 9. Replying to an existing thread

Use the `/replies` endpoint — not a new top-level comment — so the
thread stays collapsed in the UI.

```bash
gh api "repos/$REPO/pulls/$PR/comments/$COMMENT_ID/replies" \
  --method POST \
  -f body="Thanks — landed in 0afc231. Re-run the e2e suite when you get a chance."
```

Common reply shapes:

- **Acknowledging a blocker**: "Good catch — will fix in the next push."
- **Deferring**: "Agreed but out of scope. Opened `#234` to track."
- **Disagreeing**: "I don't think this is a blocker because <concrete
  reason>. Happy to discuss synchronously if you disagree."

---

## 10. Resolving a thread (GraphQL)

REST can't resolve — GraphQL can. Resolve AFTER the author has acted,
not to bury disagreement:

```bash
THREAD_ID=$(gh api graphql -f query='
query($owner:String!,$name:String!,$pr:Int!){
  repository(owner:$owner,name:$name){
    pullRequest(number:$pr){
      reviewThreads(first:100){ nodes { id isResolved comments(first:1){ nodes { path line body } } } }
    }
  }
}' -f owner="${REPO%/*}" -f name="${REPO#*/}" -F pr="$PR" \
  --jq '.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved==false) | .id' | head -n1)

gh api graphql -f query='
mutation($thread: ID!) { resolveReviewThread(input: {threadId: $thread}) { thread { id } } }
' -f thread="$THREAD_ID"
```

---

## 11. Posting a review from JSON you already have

If you're posting the output of the `pr-review` smart-prompt chain,
the chain already emits Markdown in the "8b. Request changes" shape.
Paste it as the `body`, then convert each finding with a `file:line`
into an entry in `comments[]`. A small `jq` helper:

```bash
# Given chain output saved as /tmp/verdict.md and per-finding JSON in
# /tmp/findings.json (array of {path, line, severity, message, suggestion?}):

jq -n \
  --arg body "$(cat /tmp/verdict.md)" \
  --slurpfile f /tmp/findings.json \
  --arg event REQUEST_CHANGES \
  '{
    event: $event,
    body: $body,
    comments: ($f[0] | map({
      path, line, side: "RIGHT",
      body: ("**" + (.severity | ascii_upcase) + "** — " + .message + (if .suggestion then "\n\n```suggestion\n" + .suggestion + "\n```" else "" end))
    }))
  }' > /tmp/review.json

gh api "repos/$REPO/pulls/$PR/reviews" --method POST --input /tmp/review.json
```

---

## Anti-patterns (avoid)

- **Drip-posting** individual line comments instead of a single review.
  Floods the author's inbox; can't be "requested changes" atomically.
- **Vague blockers** ("this looks wrong"). A blocker must name a
  concrete failure mode and cite `file:line`.
- **Nit storms** on a blocking review. Nits go in a `COMMENT` review,
  or under a `## Nits` section when there are also real findings.
- **Suggestions that don't apply cleanly.** Test the block mentally: if
  GitHub replaced `line` with this, would it compile?
- **Secrets in comments.** If you see a token in the diff, truncate to
  4 chars (`gho_ab…`) and flag it as a blocker; don't echo it.
