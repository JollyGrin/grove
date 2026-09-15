#!/usr/bin/env python3
"""Append to TASKS.md / LEARNINGS.md without reading them, and keep the heads
under their caps by archiving (never deleting) the oldest rows.

    scripts/log-append.py tasks < row.md
    scripts/log-append.py learnings --section "Go / CLI" < entry.md
    scripts/log-append.py rotate            # no append; just enforce the caps

A row/entry is a top-level markdown bullet (`- [x] …` / `- **YYYY-MM-DD · …`)
with its indented continuation lines. `tasks` inserts at the top of §Now
(newest first); `learnings` inserts at the top of the named section. After
either, and under `rotate`, every head over its cap has its OLDEST rows
moved into docs/archive/<FILE>-YYYY-MM.md (the row's own month; same format,
newest first) until the head fits. The cap is the `<!-- head-cap: N -->`
marker in each head; internal/guidance's test enforces the same marker, so a
head that grows past it fails `go test ./...`.

Nothing is ever deleted: every row moved out of a head lands in an archive.
"""
import os, re, sys

# LOG_ROOT overrides the repo root (tests run the script against a scratch copy).
ROOT = os.environ.get('LOG_ROOT') or os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ARCHIVE = os.path.join(ROOT, 'docs', 'archive')
DATE = re.compile(r'20\d\d-\d\d-\d\d')
CAP = re.compile(r'<!--\s*head-cap:\s*(\d+)')


def read(p):
    with open(p, encoding='utf-8') as f:
        return f.read()


def write(p, s):
    with open(p, 'w', encoding='utf-8') as f:
        f.write(s)


def cap_of(text, name):
    m = CAP.search(text)
    if not m:
        sys.exit(f'{name}: no `<!-- head-cap: N -->` marker — refusing to guess')
    return int(m.group(1))


def blocks(lines, start, cont):
    """Split lines into top-level blocks: ('block', [lines]) for a bullet plus
    its continuation, ('other', [line]) for anything else."""
    out, cur = [], None
    for ln in lines:
        if start.match(ln):
            if cur is not None:
                out.append(('block', cur))
            cur = [ln]
        elif cur is not None and cont.match(ln):
            cur.append(ln)
        else:
            if cur is not None:
                out.append(('block', cur))
                cur = None
            out.append(('other', [ln]))
    if cur is not None:
        out.append(('block', cur))
    return out


def row_date(text):
    m = (re.search(r'grove-\d+,\s*(20\d\d-\d\d-\d\d)', text)
         or re.search(r'\((?:[^()]*?,\s*)?(20\d\d-\d\d-\d\d)\)', text)
         or DATE.search(text))
    return m.group(1) if m else None


def entry_date(text):
    m = DATE.search(text.split('\n', 1)[0])
    return m.group(0) if m else None


# ---------------------------------------------------------------- archives

def archive_header(kind, month):
    if kind == 'tasks':
        return (f'# Grove — status board archive · {month}\n\n'
                '> Shipped rows rotated out of [TASKS.md](../../TASKS.md) §Now by\n'
                '> `scripts/log-append.py` (grove-275). Same row format, newest first.\n'
                '> Grep `TASKS.md docs/archive/TASKS-*.md` for the full shipped log.\n\n'
                '## Shipped\n\n')
    return (f'# Grove — learnings archive · {month}\n\n'
            '> Rotated out of [LEARNINGS.md](../../LEARNINGS.md) by\n'
            '> `scripts/log-append.py` (grove-275). Same entry format and sections;\n'
            '> newest first within each section. The rules these entries taught live\n'
            '> in `.claude/skills/`; this is the dated record. Grep `LEARNINGS.md\n'
            '> docs/archive/LEARNINGS-*.md` for the full log.\n')


def archive_insert(kind, month, section, block_text):
    """Put block_text at the TOP of its section in the month's archive: a row
    leaving a head is newer than everything already archived for that month."""
    os.makedirs(ARCHIVE, exist_ok=True)
    p = os.path.join(ARCHIVE, f'{"TASKS" if kind == "tasks" else "LEARNINGS"}-{month}.md')
    text = read(p) if os.path.exists(p) else archive_header(kind, month)
    anchor = '## Shipped\n' if kind == 'tasks' else section + '\n'
    i = text.find('\n' + anchor)
    if i < 0:
        if kind == 'tasks':
            sys.exit(f'{p}: no `## Shipped` section')
        text = text.rstrip('\n') + '\n\n' + section + '\n\n'
        i = text.find('\n' + anchor)
    j = i + 1 + len(anchor)
    # skip the blank line after the heading
    while j < len(text) and text[j] == '\n':
        j += 1
    text = text[:j] + block_text + '\n' + text[j:]
    write(p, text)


# ---------------------------------------------------------------- TASKS.md

TASK_START = re.compile(r'^- \[')
TASK_CONT = re.compile(r'^      ')


def tasks_split(text):
    """-> (before_now, now_body_lines, after_now) where after_now starts at the
    next `## ` heading after `## Now`."""
    lines = text.split('\n')
    i = next(k for k, l in enumerate(lines) if l.startswith('## Now'))
    j = next((k for k in range(i + 1, len(lines)) if lines[k].startswith('## ')), len(lines))
    return lines[:i + 1], lines[i + 1:j], lines[j:]


def tasks_append(text, row):
    head, body, tail = tasks_split(text)
    row = row.rstrip('\n')
    if not TASK_START.match(row):
        sys.exit('a TASKS.md row must start with `- [x] ` or `- [ ] `')
    # body starts with a blank line after the heading
    lead = ['']
    while body and body[0] == '':
        body = body[1:]
    return '\n'.join(head + lead + [row] + body + tail)


def tasks_rotate(text, cap):
    """Move the oldest shipped rows out of §Now until the head fits."""
    moved = 0
    while len(text.encode('utf-8')) > cap:
        head, body, tail = tasks_split(text)
        bl = blocks(body, TASK_START, TASK_CONT)
        # candidates: shipped rows, oldest = last in file order (newest first)
        idx = [k for k, (kind, b) in enumerate(bl) if kind == 'block' and b[0].startswith('- [x]')]
        if not idx:
            sys.exit(f'TASKS.md is over its cap ({cap}) but §Now has no shipped rows to archive')
        k = idx[-1]
        b = bl[k][1]
        while b and b[-1] == '':
            b.pop()
        row_text = '\n'.join(b)
        d = row_date(row_text)
        if not d:
            sys.exit('undated shipped row cannot be archived: ' + b[0])
        archive_insert('tasks', d[:7], None, row_text)
        del bl[k]
        body = [ln for _, blk in bl for ln in blk]
        text = '\n'.join(head + body + tail)
        text = re.sub(r'\n{3,}', '\n\n', text)
        moved += 1
    return text, moved


# ------------------------------------------------------------- LEARNINGS.md

ENTRY_START = re.compile(r'^- ')
ENTRY_CONT = re.compile(r'^  ')


def learn_sections(text):
    """-> (preamble_lines, [(heading, body_lines)])"""
    lines = text.split('\n')
    idx = [k for k, l in enumerate(lines) if l.startswith('## ')]
    pre = lines[:idx[0]]
    secs = []
    for n, i in enumerate(idx):
        end = idx[n + 1] if n + 1 < len(idx) else len(lines)
        secs.append((lines[i], lines[i + 1:end]))
    return pre, secs


def learn_join(pre, secs):
    out = list(pre)
    for h, body in secs:
        out += [h] + body
    return '\n'.join(out)


def learn_append(text, section, entry):
    entry = entry.rstrip('\n')
    if not ENTRY_START.match(entry):
        sys.exit('a LEARNINGS.md entry must start with `- **YYYY-MM-DD · `')
    pre, secs = learn_sections(text)
    names = [h[3:].strip() for h, _ in secs]
    if section not in names:
        sys.exit(f'no section {section!r}; sections are: ' + ' | '.join(names))
    k = names.index(section)
    h, body = secs[k]
    while body and body[0] == '':
        body = body[1:]
    secs[k] = (h, ['', entry] + body)
    return learn_join(pre, secs)


def learn_rotate(text, cap):
    moved = 0
    while len(text.encode('utf-8')) > cap:
        pre, secs = learn_sections(text)
        oldest = None  # (date, section index, block index, block)
        parsed = []
        for si, (h, body) in enumerate(secs):
            bl = blocks(body, ENTRY_START, ENTRY_CONT)
            parsed.append(bl)
            for bi, (kind, b) in enumerate(bl):
                if kind != 'block':
                    continue
                d = entry_date('\n'.join(b)) or '0000-00-00'
                if oldest is None or d < oldest[0]:
                    oldest = (d, si, bi, b)
        if oldest is None:
            sys.exit(f'LEARNINGS.md is over its cap ({cap}) but has no entries to archive')
        d, si, bi, b = oldest
        if d == '0000-00-00':
            sys.exit('undated entry cannot be archived: ' + b[0])
        while b and b[-1] == '':
            b.pop()
        archive_insert('learnings', d[:7], secs[si][0], '\n'.join(b))
        del parsed[si][bi]
        secs[si] = (secs[si][0], [ln for _, blk in parsed[si] for ln in blk])
        text = re.sub(r'\n{3,}', '\n\n', learn_join(pre, secs))
        moved += 1
    return text, moved


# ------------------------------------------------------------------- main

def enforce(name, rotate_fn):
    p = os.path.join(ROOT, name)
    text = read(p)
    cap = cap_of(text, name)
    new, moved = rotate_fn(text, cap)
    if moved:
        write(p, new)
    size = len(new.encode('utf-8'))
    print(f'{name}: {size:,} bytes (cap {cap:,}); archived {moved}')


def main(argv):
    if len(argv) < 2 or argv[1] not in ('tasks', 'learnings', 'rotate'):
        sys.exit(__doc__)
    cmd = argv[1]
    if cmd == 'tasks':
        row = sys.stdin.read()
        p = os.path.join(ROOT, 'TASKS.md')
        write(p, tasks_append(read(p), row))
        print('TASKS.md: row added at the top of §Now')
    elif cmd == 'learnings':
        if len(argv) != 4 or argv[2] != '--section':
            sys.exit('usage: log-append.py learnings --section "<heading text>" < entry.md')
        entry = sys.stdin.read()
        p = os.path.join(ROOT, 'LEARNINGS.md')
        write(p, learn_append(read(p), argv[3], entry))
        print(f'LEARNINGS.md: entry added at the top of §{argv[3]}')
    enforce('TASKS.md', tasks_rotate)
    enforce('LEARNINGS.md', learn_rotate)


if __name__ == '__main__':
    main(sys.argv)
