import json, glob, os, re, sys, collections, statistics, datetime
roots = [os.path.expanduser('~/.claude/projects'), os.path.expanduser('~/.cc-work/projects')]
rows = []
def classify(d):
    n = os.path.basename(d)
    if n.startswith('-private-tmp') or n in ('-',): return ('scratch','scratch')
    kind = 'other'
    m = re.search(r'-Users-grins-git-(.+?)--grove-orchestrator(.*)$', n)
    if m: return (m.group(1), 'orchestrator')
    m = re.search(r'--worktrees-([^-]+)-', n)
    if m: return (m.group(1), 'worker')
    return (n.replace('-Users-grins-','')[:30], 'other')
for root in roots:
    for d in glob.glob(root + '/*'):
        ws, kind = classify(d)
        for f in glob.glob(d + '/*.jsonl'):
            first = None; n_calls = 0; seen = set(); total_ctx = 0; first_user_chars = 0; got_user = False; ts = None; compact_first = False; last_ctx = 0
            try:
                for i, l in enumerate(open(f, errors='replace')):
                    try: L = json.loads(l)
                    except: continue
                    if L.get('isSidechain'): continue
                    t = L.get('type')
                    if i < 3 and t == 'summary': compact_first = True
                    m = L.get('message') or {}
                    if t == 'user' and first is None:
                        c = m.get('content')
                        first_user_chars += len(c) if isinstance(c, str) else len(json.dumps(c or ''))
                    if t == 'assistant':
                        u = m.get('usage'); key = (m.get('id'), L.get('requestId'))
                        if not u or key in seen: continue
                        if m.get('model') == '<synthetic>': continue
                        seen.add(key); n_calls += 1
                        ctx = u.get('input_tokens',0) + u.get('cache_creation_input_tokens',0) + u.get('cache_read_input_tokens',0)
                        total_ctx += ctx; last_ctx = ctx
                        if first is None:
                            first = dict(ctx=ctx, inp=u.get('input_tokens',0), cw=u.get('cache_creation_input_tokens',0), cr=u.get('cache_read_input_tokens',0), model=m.get('model'), ts=L.get('timestamp'))
            except Exception as e:
                continue
            if first and first['ctx'] > 0:
                rows.append(dict(ws=ws, kind=kind, root=os.path.basename(os.path.dirname(root)), file=f, calls=n_calls, total=total_ctx, last=last_ctx, fuc=first_user_chars, **first))
json.dump(rows, open('boot.json','w'))
print(len(rows), 'sessions')
def pct(v, p):
    v = sorted(v); return v[min(len(v)-1, int(len(v)*p))]
def table(keyf, rows, title, minn=3):
    print('\n##', title)
    g = collections.defaultdict(list)
    for r in rows: g[keyf(r)].append(r)
    print('%-44s %5s %7s %7s %7s %7s %6s %6s %8s' % ('group','n','p50','mean','p90','max','cr%','1call','boot%tot'))
    for k, v in sorted(g.items(), key=lambda kv: -len(kv[1])):
        if len(v) < minn: continue
        c = [r['ctx'] for r in v]
        cr = sum(r['cr'] for r in v) / max(1, sum(c))
        one = sum(1 for r in v if r['calls'] <= 2) / len(v)
        share = sum(c) / max(1, sum(r['total'] for r in v))
        print('%-44s %5d %7d %7d %7d %7d %5.0f%% %5.0f%% %7.1f%%' % (str(k)[:44], len(v), pct(c,.5), statistics.mean(c), pct(c,.9), max(c), cr*100, one*100, share*100))
real = [r for r in rows if r['kind'] in ('worker','orchestrator')]
table(lambda r: (r['kind'], r['ws']), real, 'first-request context by kind/workspace')
table(lambda r: (r['kind'], (r['ts'] or '')[:7]), real, 'by kind/month')
table(lambda r: (r['kind'], r['model']), real, 'by kind/model')
recent = [r for r in real if (r['ts'] or '') >= '2026-09-14']
table(lambda r: (r['kind'], r['ws']), recent, 'LAST 14 DAYS by kind/workspace', 1)
table(lambda r: (r['kind'], r['model']), recent, 'LAST 14 DAYS by kind/model', 1)
