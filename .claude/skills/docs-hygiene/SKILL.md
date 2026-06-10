---
name: docs-hygiene
description: Restructure and maintain Markdown documentation for section-level navigability and zero redundancy — canonical section index, stable GitHub anchors, targeted deep links, single-source-of-truth, and offline link/anchor checking (lychee in CI plus a local make target). Use this whenever the user wants to reorganize or clean up docs, create or update a docs index, remove duplicated documentation, slim down CLAUDE.md or other agent files, fix broken Markdown links or anchors, or add documentation link checking — even if they only say "the docs are a mess", "make the docs navigable", or "where should this be documented".
---

# Docs Hygiene

Make a repository's Markdown documentation findable at the **section** level and
free of duplicated content, then make that state enforceable so it cannot rot.

The core problem this solves: docs split per topic are easy to write but hard to
navigate — a reader hunting one fact must scan whole files, and the same fact
copy-pasted into README, CLAUDE.md and a guide will drift into three conflicting
versions. The fix is structural, not editorial: one canonical map, one home per
fact, links everywhere else, and CI that fails when a link breaks.

## Principles

Apply these in every decision. They matter more than any specific step below.

1. **One canonical full map.** Exactly one file (usually `docs/README.md`) lists
   every guide and every `##` section as an anchor link. No second full map
   anywhere — two maps always drift apart. Other files link *selectively*.

2. **One fact, one place.** Every piece of content lives in exactly one doc;
   everything else links to it. When you find the same information in two files,
   pick the canonical home (the most specific doc that owns the topic) and
   replace the other copy with a link. Agent files (CLAUDE.md) keep inline only
   what no public doc covers — code-level contracts, local-only tooling.

3. **Headings are an anchor contract.** GitHub auto-generates anchors from
   headings, so renaming a heading silently breaks every inbound link. Choose
   heading names to be stable, prefer plain words over punctuation-heavy titles
   (punctuation is dropped from the slug, producing fragile anchors), never add
   HTML `<a id>` clutter, and put a one-line warning in the index header:
   "renaming a heading breaks links — update this map when adding sections."

4. **Cross-cutting facts get one section, referenced from every mention.**
   When several docs say "a warning is logged" or "see the logs", the *where and
   how* lives in one anchored section (usually in troubleshooting) and every
   mention links to it. One place to update when the mechanism changes.

5. **Troubleshooting = one `##` section per failure mode.** A flat bullet list
   has nothing to anchor to. Each failure mode gets a section with symptom →
   diagnosis → fix; multi-cause problems get an ordered checklist (most likely
   cause first), each step linking to the relevant doc section.

6. **Agent doc maps are tiny and task-oriented.** If the repo has a CLAUDE.md
   (or similar), give it a "question → section link" table covering the
   questions an agent actually hunts for — at most ~8 rows, and **fewer when
   the doc set is small**: a 3-guide repo does not need 8 rows, and padding the
   table to the cap just re-creates the full map (violating principle 1) in a
   more verbose shape. The goal of slimming an agent file is less content, not
   the same content rewritten as links.

7. **Enforce, don't hope.** Section links without CI validation rot silently.
   Add an offline link check (lychee) on PRs and main, and a local make target
   mirroring it. Offline mode validates relative links and `#fragment` anchors
   without network flakiness; external URLs must never block a PR.

## Workflow

### 1. Inventory

Map the current state before touching anything:

```sh
ls *.md docs/ 2>/dev/null
grep -n '^## ' README.md CLAUDE.md docs/*.md 2>/dev/null   # heading inventory
grep -rno '\](\([^)]*\.md[^)]*\))' --include='*.md' . | head -50  # existing links
```

Note: files with zero `##` headings (nothing to anchor to), content that appears
in more than one file, whether a docs index exists, and whether any link check
runs in CI.

### 2. Stabilize headings and fill gaps

Restructure flat files into `##` sections *first* — the index will anchor to
them, so the headings must exist and be stable before you build the map. While
adding headings, fill obvious content gaps that need the same headings (a
missing troubleshooting case, an undocumented reserved name) rather than doing
a second pass later.

### 3. Deduplicate

For each duplicated fact, decide the canonical home and replace other copies
with links. Direction of consolidation: README stays a shopfront (short +
links), topic guides own their topic, contributor/dev content gets its own
guide (`docs/development.md`) if it is scattered, agent files become pointers.
If a doc the canonical home should link to is missing a section, add the
section — do not leave the fact homeless.

### 4. Build the canonical index

In `docs/README.md` (or the repo's docs index): a table of guides with a
one-word type hint (how-to / reference / explanation), then per guide the full
list of `##` sections as anchor links. Add the heading-rename warning to the
header. GitHub slug rules (what `## Heading` becomes in `#anchor`): lowercase;
spaces → `-`; backticks, punctuation and symbols dropped; letters, digits, `-`
and `_` kept; duplicate slugs get `-1`, `-2` suffixes. Em dashes vanish, so
`Step 1 — install` → `step-1--install` (two hyphens from the surrounding
spaces). Don't hand-verify — the checker in step 6 does it.

### 5. Add selective deep links

- Root README: keep file-level links; add a handful of high-traffic deep links
  (quick start → the main reference table, "something broke" → the
  troubleshooting checklist entry point).
- CLAUDE.md / agent files: the question → section table (principle 6).

### 6. Enforce and verify

If the repo lacks a link check, add both. Adjust the file list
(`README.md CLAUDE.md docs`) to what actually exists in the repo.

**CI job** (`.github/workflows/docs.yml` or an existing workflow):

```yaml
- uses: lycheeverse/lychee-action@v2
  with:
    args: --offline --include-fragments --no-progress README.md CLAUDE.md docs
    fail: true
```

Triggers: every PR and push to the default branch. `--offline` skips external
URLs entirely, so third-party downtime never blocks a PR.

**Make target** (mirror the CI flags so local == CI):

```make
check-links:
	docker run --rm -w /input -v "$(CURDIR)":/input lycheeverse/lychee:latest \
		--offline --include-fragments --no-progress README.md CLAUDE.md docs
```

Then verify with two independent checks — lychee, plus the bundled checker
(independent slug implementation, catches anything a single tool misses):

```sh
make check-links   # or the docker run directly
python3 scripts/check_anchors.py README.md CLAUDE.md docs/
```

Both must pass before you report done. Sandboxes often refuse the `-v` bind
mount into Docker — don't burn time fighting it: pipe the files in instead
(`tar c README.md docs | docker run --rm -i lycheeverse/lychee - …` or
`docker create` + `docker cp`), or fall back to the bundled checker alone and
say so explicitly in your report. The `-v` mount in the make target is still
correct for normally configured hosts.

## Reporting

When done, report: which facts moved where (old location → canonical home),
what the link topology is now (who links what), and the verification result
(link counts, errors). The user should be able to audit every consolidation
decision without diffing the files.
