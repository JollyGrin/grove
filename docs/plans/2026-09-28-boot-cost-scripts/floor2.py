import json, collections, statistics
rows = json.load(open('boot.json'))
CPT = 3.6
def textlen(a):
    t = a.get('type')
    if t in ('deferred_tools_delta','agent_listing_delta'): return len('\n'.join(a.get('addedLines') or []))
    if t == 'instructions': return sum(len(json.dumps(f)) for f in a.get('files') or [])
    if t == 'prompt_snapshot': return len(''.join(a.get('systemPrompt') or []))
    for k in ('content','text','context'):
        if isinstance(a.get(k), str): return len(a[k])
    return len(json.dumps(a))
def decomp(f):
    p = collections.Counter(); img = 0
    for l in open(f, errors='replace'):
        try: L = json.loads(l)
        except: continue
        if L.get('isSidechain'): continue
        t = L.get('type')
        if t == 'assistant' and (L.get('message') or {}).get('usage'): break
        if t == 'attachment':
            a = L.get('attachment') or {}
            if a.get('type') == 'instructions':
                for fl in a.get('files') or []:
                    path = fl.get('path') or ''
                    k = 'memory index (MEMORY.md)' if 'MEMORY.md' in path else 'orchestrator brain' if 'orchestrator/CLAUDE.md' in path else 'repo CLAUDE.md/AGENTS.md'
                    p[k] += len(json.dumps(fl))
            else: p[{'prompt_snapshot':'system prompt','skill_listing':'skill listing','deferred_tools_delta':'deferred tool names (MCP)','agent_listing_delta':'agent listing','mcp_instructions_delta':'MCP server instructions'}.get(a.get('type'), 'other attachments')] += textlen(a)
        elif t == 'user':
            c = (L.get('message') or {}).get('content')
            if isinstance(c, str): p['first prompt / kickoff'] += len(c)
            else:
                for b in c or []:
                    if b.get('type') == 'text': p['first prompt / kickoff'] += len(b.get('text',''))
                    elif b.get('type') == 'image': img += 1
    return p, img
out = {}
for kind, ws in [('worker','grove'),('orchestrator','grove'),('worker','unbrewed'),('orchestrator','unbrewed'),('worker','thegrid'),('orchestrator','thegrid'),('worker','waterhouse'),('orchestrator','waterhouse')]:
    c = [r for r in rows if r['kind']==kind and r['ws']==ws and (r['ts'] or '')>='2026-09-14' and r['model'].startswith('claude')]
    if not c: continue
    agg = collections.Counter(); n = 0; ctxs = []
    for r in c:
        p, img = decomp(r['file'])
        if img: continue
        n += 1; ctxs.append(r['ctx']); agg.update(p)
    m = statistics.mean(ctxs); acc = 0
    print('\n### %s/%s n=%d (image-free) mean=%.0fk p50=%.0fk' % (kind, ws, n, m/1000, statistics.median(ctxs)/1000))
    for k, v in agg.most_common():
        tok = v/n/CPT; acc += tok
        if tok >= 200: print('  %-30s %5.1fk %4.0f%%' % (k, tok/1000, 100*tok/m))
    print('  %-30s %5.1fk %4.0f%%' % ('tool schemas (residual)', (m-acc)/1000, 100*(m-acc)/m))
# economics
print('\n## restart economics (per-MTok list: input price P; cache write 1h = 2P, read = 0.1P; fable-5-1 read 0.025P)')
for boot, big in [(46,150),(46,250),(60,180),(91,170),(76,276)]:
    for name, rd in [('std 0.1x', .1), ('fable5.1 0.025x', .025)]:
        # restart cost in input-token-equivalents: worst case boot all cache-write 2x + 15k reorientation written 2x; saving per turn = (big - boot - 15) * rd
        worst = boot*2 + 15*2; typical = boot*0.5*2 + boot*0.5*rd + 15*2
        save = (big - boot - 15) * rd
        print('boot %3dk vs bloated %3dk [%s]: restart costs %3.0f–%3.0fk in-equiv; saves %.1fk/turn → breakeven %2.0f–%2.0f turns' % (boot, big, name, typical, worst, save, typical/save, worst/save))
