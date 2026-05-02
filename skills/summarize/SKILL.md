---
name: summarize
description: Summarize URLs, articles, PDFs, and YouTube videos using the agent’s built-in LLM — no external CLI required.
metadata:
  {
    “opsintelligence”:
      {
        “emoji”: “🧾”,
      },
  }
---

# Summarize

Summarize URLs, articles, PDFs, YouTube videos, and any pasted text using the agent’s LLM directly.
No external CLI dependency — works out of the box.

## When to use (trigger phrases)

Use this skill immediately when the user asks any of:

- “summarize this URL / article / page”
- “what’s this link about?”
- “give me the key points from this PDF”
- “transcribe / summarize this YouTube video”
- “tldr this”

## How to summarize

### URL or article
Fetch the page with the `xurl` tool (or `exec curl -s <url>`), then summarize the returned text.

### YouTube video
Use `exec yt-dlp --write-auto-sub --sub-format vtt --skip-download -o /tmp/yt %(url)s` to pull captions, read the `.vtt` file, strip timestamps, and produce a summary. If `yt-dlp` is unavailable, fetch the page HTML and extract the `<title>` + description as a best-effort fallback.

### PDF or local file
Read the file with the `read_file` tool (or `exec pdftotext`), then summarize the extracted text.

### Pasted text
Summarize directly — no tool needed.

## Output format

- Default: bullet-point key takeaways + one-sentence TL;DR at the top
- If the user specifies a length (short / medium / long), match it
- For transcripts: lead with a tight summary, then offer to expand a specific section
