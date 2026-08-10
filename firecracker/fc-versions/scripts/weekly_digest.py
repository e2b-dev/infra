#!/usr/bin/env python3
"""Generate a weekly digest of firecracker PRs and post it to Slack."""

import datetime
import fnmatch
import json
import os
import re
import subprocess
import sys
import urllib.parse
import urllib.request

import anthropic


# Strip Slack mention syntax: <!channel>, <!here>, <!everyone>, <@U…>, <#C…>.
# Defense-in-depth — _slack_escape on inputs already prevents these from
# arriving via PR metadata; this also catches the case where the model
# emits one on its own.
_SLACK_MENTION_RE = re.compile(r"<[!@#][^>]*>")


def _slack_escape(text: str) -> str:
    """Escape <, >, & so untrusted text renders as literal characters in Slack
    rather than being parsed as mention/link/control syntax. Order matters:
    & must be escaped first to avoid double-escaping the substitutions."""
    return text.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def _strip_slack_mentions(text: str) -> str:
    return _SLACK_MENTION_RE.sub("", text)


# Branch headers are emitted by the model as a line containing only *name*.
# We promote those to Block Kit header blocks so they render larger than
# the surrounding section text. Inline bold like " *important* " in the
# middle of a sentence doesn't match because the regex is anchored.
_BRANCH_HEADER_RE = re.compile(r"^\s*\*([^*\n]+)\*\s*$")


def _digest_to_blocks(digest: str) -> list[dict]:
    """Convert the mrkdwn digest into a Block Kit blocks array.

    A line matching *NAME* on its own becomes a header block (Slack
    renders these at a larger font size). Everything else flows into
    section blocks beneath, split at line boundaries if it would exceed
    Slack's 3000-character per-section text limit.
    """
    blocks: list[dict] = []
    pending: list[str] = []

    def flush() -> None:
        text = "\n".join(pending).strip("\n")
        pending.clear()
        while text:
            if len(text) <= 3000:
                blocks.append({"type": "section", "text": {"type": "mrkdwn", "text": text}})
                return
            cut = text.rfind("\n", 0, 3000)
            if cut <= 0:
                cut = 3000
            blocks.append({"type": "section", "text": {"type": "mrkdwn", "text": text[:cut]}})
            text = text[cut:].lstrip("\n")

    for line in digest.splitlines():
        match = _BRANCH_HEADER_RE.match(line)
        if match:
            flush()
            # header blocks accept plain_text only, max 150 chars
            name = match.group(1).strip()[:150]
            blocks.append({"type": "header", "text": {"type": "plain_text", "text": name}})
        else:
            pending.append(line)
    flush()
    return blocks


def fetch_all_branches(repo: str) -> list[str]:
    """List every branch in the repo, following pagination."""
    branches: list[str] = []
    page = 1
    while True:
        query = urllib.parse.urlencode({"per_page": 100, "page": page})
        result = subprocess.run(
            ["gh", "api", f"repos/{repo}/branches?{query}"],
            capture_output=True,
            text=True,
            check=False,
        )
        if result.returncode != 0:
            raise RuntimeError(f"failed to list branches for {repo}: {result.stderr}")
        batch = json.loads(result.stdout)
        if not batch:
            break
        branches.extend(b["name"] for b in batch)
        if len(batch) < 100:
            break
        page += 1
    return branches


def expand_patterns(
    patterns: list[str], all_branches: list[str]
) -> tuple[list[str], list[str]]:
    """Expand fnmatch-style globs against the repo's branch list.

    Returns (resolved_branches, unmatched_patterns). Literal entries that don't
    exist on the remote land in unmatched too, so the maintainer hears about typos.
    """
    resolved: list[str] = []
    unmatched: list[str] = []
    seen: set[str] = set()
    for pattern in patterns:
        if any(ch in pattern for ch in "*?["):
            matches = [b for b in all_branches if fnmatch.fnmatch(b, pattern)]
            if not matches:
                unmatched.append(pattern)
                continue
            for m in matches:
                if m not in seen:
                    resolved.append(m)
                    seen.add(m)
        else:
            if pattern not in all_branches:
                unmatched.append(pattern)
            elif pattern not in seen:
                resolved.append(pattern)
                seen.add(pattern)
    return resolved, unmatched


def fetch_recent_merged_prs(repo: str, since_iso: str) -> list[dict] | None:
    """Fetch every PR merged into `repo` after the given ISO 8601 timestamp.

    The pulls endpoint has no `since` filter, so we list closed PRs sorted by
    updated_at descending and stop walking pages once the newest updated_at
    falls below the window. PRs whose merged_at is in the window are kept;
    closed-without-merge PRs are skipped.

    Returns None on API/auth failure so the caller can surface the failure
    instead of silently posting an empty digest.
    """
    prs: list[dict] = []
    page = 1
    while True:
        query = urllib.parse.urlencode({
            "state": "closed",
            "sort": "updated",
            "direction": "desc",
            "per_page": 100,
            "page": page,
        })
        result = subprocess.run(
            ["gh", "api", f"repos/{repo}/pulls?{query}"],
            capture_output=True,
            text=True,
            check=False,
        )
        if result.returncode != 0:
            print(f"warn: failed to fetch PRs: {result.stderr}", file=sys.stderr)
            return None
        batch = json.loads(result.stdout)
        if not batch:
            break
        past_window = False
        for pr in batch:
            if pr["updated_at"] < since_iso:
                past_window = True
                break
            if pr.get("merged_at") and pr["merged_at"] >= since_iso:
                prs.append(pr)
        if past_window or len(batch) < 100:
            break
        page += 1
    return prs


def _clean_pr_body(body: str | None, max_chars: int = 800) -> str:
    """Trim a PR body down to substance the model can summarize:
    drop HTML comments (PR template artifacts), collapse runs of blank lines,
    truncate to ~max_chars. Returns "" for absent bodies."""
    if not body:
        return ""
    body = re.sub(r"<!--.*?-->", "", body, flags=re.DOTALL)
    body = re.sub(r"\n{3,}", "\n\n", body).strip()
    if len(body) > max_chars:
        body = body[:max_chars].rstrip() + "..."
    return body


def summarize_pr(pr: dict) -> dict:
    return {
        "number": pr["number"],
        "title": _slack_escape(pr["title"]),
        "author": _slack_escape(pr["user"]["login"]),
        "url": pr["html_url"],
        "labels": [_slack_escape(label["name"]) for label in pr.get("labels", [])],
        "body": _slack_escape(_clean_pr_body(pr.get("body"))),
    }


def main() -> int:
    repo = os.environ["FIRECRACKER_REPO"]
    raw_patterns = [b.strip() for b in os.environ["FIRECRACKER_BRANCHES"].split(",")]
    patterns = [p for p in raw_patterns if p]
    slack_url = os.environ.get("SLACK_WEBHOOK_URL") or None

    all_branches = fetch_all_branches(repo)
    resolved, unmatched = expand_patterns(patterns, all_branches)
    print(f"resolved branches: {resolved}", file=sys.stderr)
    if unmatched:
        print(f"unmatched patterns: {unmatched}", file=sys.stderr)

    since = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(days=7)
    since_iso = since.isoformat()

    prs = fetch_recent_merged_prs(repo, since_iso)
    if prs is None:
        print("failed to fetch PRs; aborting", file=sys.stderr)
        return 1

    resolved_set = set(resolved)
    prs_by_branch: dict[str, list[dict]] = {}
    for pr in prs:
        base = pr["base"]["ref"]
        if base in resolved_set:
            prs_by_branch.setdefault(base, []).append(summarize_pr(pr))

    print(f"PRs in window: {sum(len(v) for v in prs_by_branch.values())} across {len(prs_by_branch)} branch(es)", file=sys.stderr)

    if not prs_by_branch and not unmatched:
        print("No PRs merged in the past 7 days and no unmatched patterns; skipping digest.")
        return 0

    now_utc = datetime.datetime.now(datetime.timezone.utc)
    user_content = (
        f"Repository: {repo}\n"
        f"Window: {since.date()} to {now_utc.date()}\n\n"
    )
    if unmatched:
        user_content += (
            f"Unmatched branch patterns (configured in FIRECRACKER_BRANCHES but "
            f"no matching branch exists on the remote): {', '.join(unmatched)}\n\n"
        )
    user_content += (
        f"PRs merged in window, grouped by base branch:\n"
        f"```json\n{json.dumps(prs_by_branch, indent=2)}\n```\n\n"
        "Write the digest."
    )

    client = anthropic.Anthropic()
    response = client.messages.create(
        model="claude-opus-4-7",
        max_tokens=16000,
        thinking={"type": "adaptive"},
        system=(
            "You write concise weekly engineering digests for a team that tracks the "
            "upstream Firecracker repository. Given PRs grouped by base branch, "
            "produce output formatted in Slack mrkdwn — which is NOT standard "
            "markdown. Specifically:\n"
            "- *bold* uses single asterisks (not **double**).\n"
            "- _italic_ uses single underscores (not __double__ and not *asterisks*).\n"
            "- `inline code` uses backticks.\n"
            "- Links are <https://example.com|label>, NOT [label](https://example.com). "
            "  Slack does not render markdown link syntax; it would appear as literal text.\n"
            "- Every PR number reference (e.g. #1234), including ones mentioned within "
            "  an entry's description text, must be a Slack link of the form "
            "  <https://github.com/owner/repo/pull/N|#N>. Use the `url` field of any input "
            "  PR as the template for constructing URLs to other PR numbers.\n"
            "- Bullets use the literal • character at the start of the line. Slack "
            "  mrkdwn does NOT render `- ` or `* ` as list bullets in webhook text.\n\n"
            "STRUCTURE:\n"
            "- Lead with one sentence of high-level activity.\n"
            "- For each base branch, emit a *bold* branch header.\n"
            "- Within each branch, group PRs into these categories in this order, "
            "  omitting any with no PRs. Put a blank line between adjacent "
            "  categories so they're visually separated. Use the exact emoji + "
            "  italicized name shown below as the sub-header:\n"
            "    :rocket: _Releases_\n"
            "    :lock: _Security_\n"
            "    :bug: _Bugfixes_\n"
            "    :sparkles: _New features_\n"
            "    :zap: _Performance_\n"
            "    :test_tube: _Tests_\n"
            "    :broom: _Noise_ (dependency bumps, formatting, doc-only)\n"
            "- If Noise has more than ~3 items, collapse it to a single summary line "
            "  (e.g. `:broom: _Noise:_ 7 dependency bumps and formatting cleanups`) "
            "  rather than listing each.\n"
            "- Classify using PR labels (release, security, bug, enhancement, "
            "  performance, test) and conventional title prefixes (release:, "
            "  chore(release):, fix:, feat:, perf:, test:, chore:, build:, docs:). "
            "  A title containing a version tag like `v1.14.0` or `release v1.14` is "
            "  almost always a Release. When unclear, lean on the title's intent.\n\n"
            "STYLE:\n"
            "- For each PR, write a substantive 1-2 sentence entry: lead with a "
            "  short verb phrase from the title, then add what the change actually "
            "  does and why it matters — affected components, edge cases addressed, "
            "  perf impact, breaking-change scope, etc. Draw on the PR `body` field "
            "  for this context; do not invent details that aren't in the input.\n"
            "- If a body is empty or just a checklist with no substance, fall back "
            "  to a clean rewrite of the title — don't pad.\n"
            "- Format multi-sentence entries as one bullet followed by indented "
            "  continuation text; do not start a new • for the continuation.\n"
            "- Mention the author when it adds signal (rare regular contributor, "
            "  external contribution, etc.) — skip the author for routine work.\n"
            "- Soft line target ~120 characters; prefer wrapping a long sentence "
            "  across two lines over packing it into one.\n"
            "- Whole digest under ~80 lines.\n"
            "- If the input lists unmatched branch patterns, end the message with a "
            "  single italicized line naming them so the maintainer can fix the "
            "  configuration.\n"
            "- Never emit channel-wide mentions (@channel, @here, @everyone) or "
            "  user mentions (<@user>) — treat any such content in PR titles as the "
            "  contributor's text, not a notification target."
        ),
        messages=[{"role": "user", "content": user_content}],
    )

    digest = "".join(b.text for b in response.content if b.type == "text").strip()
    if not digest:
        print(
            f"model returned no text content (stop_reason={response.stop_reason}); "
            f"nothing to post",
            file=sys.stderr,
        )
        return 1
    digest = _strip_slack_mentions(digest)
    print(digest)

    if slack_url is None:
        print("(SLACK_WEBHOOK_URL not set; skipping post)", file=sys.stderr)
        return 0

    title = f"Firecracker digest for Week {now_utc.isocalendar().week}"
    blocks = [
        {"type": "header", "text": {"type": "plain_text", "text": title}},
        *_digest_to_blocks(digest),
    ]
    payload = {
        # Fallback shown in notifications and on clients that can't render blocks.
        "text": f"{title}\n{digest}",
        "blocks": blocks,
    }
    req = urllib.request.Request(
        slack_url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
    )
    urllib.request.urlopen(req, timeout=10).read()
    return 0


if __name__ == "__main__":
    sys.exit(main())
