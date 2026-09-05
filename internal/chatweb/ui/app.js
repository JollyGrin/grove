/* gv chat — the phone client (grove-218). Hand-written, no toolchain.
 *
 * Three screens, routed on the hash so the phone's back button works:
 *   #/            projects  (one card per registered workspace)
 *   #/w/<label>   that project's chats
 *   #/c/<addr>    one chat: history, live stream, composer
 *
 * The one rule that shapes the whole file: THE SERVER DECIDES, THE PAGE
 * RENDERS. Whether a chat takes input comes from the row's `writable`
 * field, never from the page's own reading of `kind`; the reason it does
 * not comes back verbatim in the API's error, never from copy invented
 * here. That is what keeps the CLI and the phone from ever disagreeing
 * about which chats are writable.
 */
'use strict';

var el = function (id) { return document.getElementById(id); };
/* `group` is the turn's open "N steps" row, or null when the last thing
 * rendered was prose (grove-261); `working` is the indicator's state. Both
 * are pure view state — nothing on the wire knows they exist. */
var view = { chats: [], profiles: [], loaded: false, es: null, maxSeq: 0, addr: null, group: null, working: false };
/* Everything the live-list loop needs: the interval handle (null means the
 * loop is deliberately stopped), a one-flight guard so a poll and a
 * refocus cannot stack fetches, and the signature of what is currently
 * painted. */
var poll = { timer: null, inflight: false, sig: null };
var POLL_MS = 5000;

/* ---------------- transport ---------------- */

/* api is every call in one place so the write gate lives in one place too:
 * the Content-Type header is what stops a page on another origin from
 * driving this server (the server answers no CORS preflight), so a POST
 * that forgets it is refused with 415 rather than quietly working. */
function api(path, body) {
  var opts = { headers: {} };
  if (body !== undefined) {
    opts.method = 'POST';
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  return fetch(path, opts).then(function (r) {
    return r.json().catch(function () { return {}; }).then(function (j) {
      if (!r.ok) throw new Error(j.error || (r.status + ' ' + r.statusText));
      document.body.classList.remove('offline');
      return j;
    });
  }, function (e) {
    /* A tailnet that dropped, a phone off wifi, the server stopped: all
     * one thing to the user, and the shell is already cached, so say
     * "not connected" instead of failing blank. */
    document.body.classList.add('offline');
    throw e;
  });
}

function loadChats() {
  return api('/api/chats').then(function (j) {
    view.chats = j.chats || [];
    view.loaded = true;
    return view.chats;
  });
}

/* The host's model profiles (grove-225). Garnish, like the picker scrape:
 * a host with none configured answers [] and the page then shows no sheet
 * at all, so a FAILED fetch degrades to the same place — `+ new chat`
 * spawns on the host default, exactly as it did before this existed. A
 * broken lane list must never be a broken new-chat button. */
function loadProfiles() {
  return api('/api/profiles').then(function (j) {
    view.profiles = j.profiles || [];
  }, function () {
    view.profiles = [];
  });
}

/* addr is how a chat is ADDRESSED on the wire: its tmux session name
 * wherever it has one, its Claude session id only when it does not (an
 * archived row has no pane). Session-name-first is deliberate — that name
 * comes straight from tmux, while an id routes through the pane/transcript
 * join, and a message delivered to the wrong chat is the failure this
 * whole subsystem is shaped around. */
function addr(c) { return c.session || c.session_id || ''; }
function chatByAddr(a) {
  for (var i = 0; i < view.chats.length; i++) if (addr(view.chats[i]) === a) return view.chats[i];
  return null;
}

/* ---------------- rendering helpers ---------------- */

function h(tag, cls, text) {
  var n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
}

function ago(iso) {
  if (!iso) return '';
  var s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (isNaN(s)) return '';
  if (s < 90) return 'just now';
  if (s < 5400) return Math.round(s / 60) + 'm ago';
  if (s < 172800) return Math.round(s / 3600) + 'h ago';
  return Math.round(s / 86400) + 'd ago';
}

/* activeAt is when the chat was last SPOKEN TO — `last_active` (the
 * transcript's mtime, on every kind since grove-228), falling back to
 * `created` when there is no transcript to read: a live pane grove cannot
 * name yet emits the zero time, which serialises as year 0001 and would
 * otherwise age as "739000d ago". `created` still means BIRTH — pane birth
 * on a live row — so it is the fallback, never the display value. */
function activeAt(c) {
  var t = c.last_active;
  if (!t || !(new Date(t).getTime() > 0)) return c.created;
  return t;
}

/* A row's label is its transcript's first prompt, which is empty until the
 * chat has said something — and empty forever for a chat grove cannot
 * identify (grove-222 leaves `session_id` null rather than guessing). Both
 * fall back to the tmux session name, which every live row has. */
function chatTitle(c) {
  return c.label || c.session || c.session_id || 'unidentified chat';
}

function showError(e) {
  var box = h('div', 'err', String(e && e.message ? e.message : e));
  el('main').prepend(box);
}

function setHeader(title, sub, back) {
  el('title').textContent = title;
  el('subtitle').textContent = sub;
  el('back').hidden = !back;
  el('back').onclick = back || null;
}

/* ---------------- screen 1: projects ---------------- */

function screenProjects() {
  setHeader('gv chat', 'orchestrator chats', null);
  el('footer').hidden = true;
  var main = el('main');
  main.textContent = '';
  var labels = [], by = {};
  view.chats.forEach(function (c) {
    if (!by[c.workspace]) { by[c.workspace] = []; labels.push(c.workspace); }
    by[c.workspace].push(c);
  });
  labels.sort();
  if (!labels.length) {
    main.append(h('div', 'empty', view.loaded
      ? 'no orchestrator chats on this machine yet'
      : 'loading…'));
    return;
  }
  labels.forEach(function (label) {
    var rows = by[label];
    var live = rows.filter(function (c) { return c.kind === 'chat'; }).length;
    var arch = rows.length - live;
    /* The card answers "where was I?", which counts alone never could:
     * the freshest activity in the project, across all three kinds. */
    var last = rows.reduce(function (best, c) {
      var t = new Date(activeAt(c)).getTime();
      return t > best ? t : best;
    }, 0);
    var b = h('button', 'row');
    b.append(h('div', 'title', label));
    var meta = h('div', 'meta');
    meta.append(h('span', '', live + ' live'), h('span', '', arch + ' other'));
    if (last > 0) meta.append(h('span', '', ago(new Date(last).toISOString())));
    if (rows.some(function (c) { return c.busy; })) meta.append(h('span', 'dot'));
    b.append(meta);
    b.onclick = function () { location.hash = '#/w/' + encodeURIComponent(label); };
    main.append(b);
  });
}

/* ---------------- screen 2: a project's chats ---------------- */

function screenWorkspace(label) {
  setHeader(label, 'chats in this project', function () { location.hash = '#/'; });
  el('footer').hidden = true;
  var main = el('main');
  main.textContent = '';

  var add = h('button', 'row new');
  add.append(h('div', 'title', '+ new chat'));
  add.append(h('div', 'meta', view.profiles.length
    ? 'spawns grove-chat-' + label + '-<n> on a backend you pick'
    : 'spawns grove-chat-' + label + '-<n> and opens it'));
  /* With profiles configured the choice is always SHOWN, never inferred —
   * the desk's own rule (grove-105: `)` opens the picker even for a lone
   * profile). Zero profiles: no sheet, straight to the host default. */
  add.onclick = function () {
    if (view.profiles.length) return openProfileSheet(label, add);
    spawnChat(label, '', add);
  };
  main.append(add);

  var rows = view.chats.filter(function (c) { return c.workspace === label; });
  if (!rows.length) { main.append(h('div', 'empty', 'no chats here yet')); return; }
  rows.forEach(function (c) {
    var b = h('button', 'row');
    b.append(h('div', 'title', chatTitle(c)));
    var meta = h('div', 'meta');
    meta.append(h('span', 'badge ' + c.kind, c.kind));
    if (c.session) meta.append(h('span', '', c.session));
    meta.append(h('span', '', ago(activeAt(c))));
    if (c.busy) meta.append(h('span', 'dot'));
    if (!c.writable) meta.append(h('span', '', 'read-only'));
    if (!c.session_id) meta.append(h('span', '', 'unidentified'));
    b.append(meta);
    b.onclick = function () { location.hash = '#/c/' + encodeURIComponent(addr(c)); };
    main.append(b);
  });
}

/* spawnChat is the one place `+ new chat` reaches the server, whichever
 * row was tapped. An empty profile sends NO profile key at all, so the
 * request is byte-identical to the one grove-218 sent and the host spawns
 * on its own Claude. */
function spawnChat(label, profile, add) {
  add.classList.add('busy');
  add.querySelector('.title').textContent = 'starting a chat…';
  var body = {};
  if (profile) body.profile = profile;
  return api('/api/workspaces/' + encodeURIComponent(label) + '/new', body)
    .then(function (j) {
      return loadChats().then(function () {
        location.hash = j.session ? '#/c/' + encodeURIComponent(j.session) : '#/w/' + encodeURIComponent(label);
        if (!j.session) render();
      });
    })
    .catch(function (e) { render(); showError(e); });
}

/* The profile sheet: the host default first, then one row per configured
 * profile, in the order the server sent them (sorted there, not here — the
 * server decides, the page renders). The names are the ONLY thing the page
 * knows about a profile; what backend each one dials stays on the host. */
function openProfileSheet(label, add) {
  var panel = el('sheet-panel');
  panel.textContent = '';
  panel.append(h('div', 'sheet-title', 'new chat in ' + label + ' — on which backend?'));
  var pick = function (profile) {
    return function () { closeSheet(); spawnChat(label, profile, add); };
  };
  var host = h('button', 'row');
  host.append(h('div', 'title', 'Claude (host default)'));
  host.append(h('div', 'meta', 'the operator’s own Claude sub'));
  host.onclick = pick('');
  panel.append(host);
  view.profiles.forEach(function (name) {
    var b = h('button', 'row');
    b.append(h('div', 'title', name));
    b.append(h('div', 'meta', 'model profile'));
    b.onclick = pick(name);
    panel.append(b);
  });
  var cancel = h('button', 'cancel', 'cancel');
  cancel.onclick = closeSheet;
  panel.append(cancel);
  var sheet = el('sheet');
  /* Tapping the dimmed backdrop dismisses it — the sheet spends money when
   * it is answered, so backing out must be the easiest thing on screen. */
  sheet.onclick = function (ev) { if (ev.target === sheet) closeSheet(); };
  sheet.hidden = false;
}

function closeSheet() { el('sheet').hidden = true; }

/* ---------------- screen 3: one chat ---------------- */

function screenChat(a) {
  var c = chatByAddr(a);
  /* Clear any half-typed draft on the way in. A composer that carries text
   * from the chat you just left is a message delivered to the wrong agent
   * one tap later — the exact failure the rest of this subsystem refuses
   * to risk (grove-116/78). */
  if (view.addr !== a) el('text').value = '';
  view.addr = a;
  var back = function () {
    location.hash = c ? '#/w/' + encodeURIComponent(c.workspace) : '#/';
  };
  setHeader(c ? chatTitle(c) : a, c ? c.workspace + ' · ' + c.kind : 'chat', back);
  var main = el('main');
  main.textContent = '';
  /* A fresh transcript starts with no open group and nothing running —
   * the stream that is about to replay decides both. */
  view.group = null;
  setWorking(false);
  if (!c) {
    main.append(h('div', 'empty', 'this chat is not in the current list — pull ⟳ to refresh'));
    el('footer').hidden = true;
    return;
  }
  composer(c);
  if (!c.session_id) {
    /* Since grove-222 a null id is an ANSWER, not a transient: grove mints
     * the id at spawn, so a chat it started carries one from second zero,
     * and a null means grove could not identify the conversation running
     * here without guessing — and it refuses to guess. There is no
     * transcript to stream, but the pane is real, so the composer stays
     * live off `writable`: this chat can be TALKED TO, just not read. */
    main.append(h('div', 'empty',
      c.kind === 'chat'
        ? 'grove cannot tell which conversation is running in this pane, so it will not guess — there is no history to show. You can still send to it. To fix the identity from a terminal: gv chat restamp ' + (c.session || '') + ' <session-id>'
        : 'no session id — nothing to read here'));
    return;
  }
  view.maxSeq = 0;
  openStream(a);
}

/* openStream replays the transcript and then follows it. Replay and live
 * append come down ONE stream, so there is no seam to get wrong. The
 * reconnect a phone waking up or a tailnet blip forces is the browser's
 * own: every entry arrives stamped `id: <seq>`, so EventSource replays that
 * back as Last-Event-ID and the server resumes past it (grove-259) — no URL
 * to rewrite here. The seq check below makes a duplicate replay harmless
 * either way. */
function openStream(a) {
  closeStream();
  var es = new EventSource('/api/chats/' + encodeURIComponent(a) + '/events');
  view.es = es;
  es.addEventListener('entry', function (ev) {
    var e;
    try { e = JSON.parse(ev.data); } catch (_) { return; }
    if (e.seq <= view.maxSeq) return;
    view.maxSeq = e.seq;
    appendEntry(e);
  });
  es.addEventListener('picker', function (ev) {
    var p;
    try { p = JSON.parse(ev.data); } catch (_) { return; }
    renderKeys(p);
  });
  es.addEventListener('fault', function (ev) {
    var msg = '';
    try { msg = JSON.parse(ev.data); } catch (_) { return; }
    showError(new Error(msg));
  });
  es.onerror = function () { document.body.classList.add('offline'); };
  es.onopen = function () { document.body.classList.remove('offline'); };
}

function closeStream() {
  if (view.es) { view.es.close(); view.es = null; }
  document.body.classList.remove('picker');
  el('keys').textContent = '';
}

/* appendEntry renders one `gv chat tail` line — the same JSON a piped CLI
 * would print. Assistant prose goes through marked; everything a turn did
 * to produce it — every tool_use, tool_result and thinking block between
 * one prose answer and the next — collapses into ONE "N steps" row
 * (grove-261). Before that, a twelve-tool turn was twelve grey rows with
 * thinking rows interleaved, so on a phone the reply sat twenty rows below
 * the question. Nothing is hidden by the grouping: the group opens to the
 * same per-entry <details> rows it always rendered.
 *
 * This is presentation only. The stream contract is untouched — every
 * entry still arrives once, in seq order, and lands in the DOM. */
function appendEntry(e) {
  var main = el('main');
  var stick = main.scrollHeight - main.scrollTop - main.clientHeight < 120;
  if (isStep(e)) {
    growGroup(main, e);
  } else {
    /* Prose — the operator's or the agent's — closes the group; the next
     * step opens a new one. That is the whole grouping rule, and it needs
     * nothing from the server: a turn's boundary is already visible in the
     * shape of the stream. */
    view.group = null;
    var node;
    if (e.role === 'assistant') {
      node = h('div', 'msg assistant');
      /* Safe because of the server's Content-Security-Policy, not because
       * marked sanitizes (it does not, since v8). See index.html's note. */
      node.innerHTML = window.marked ? window.marked.parse(e.text || '') : '';
      if (!window.marked) node.textContent = e.text || '';
    } else {
      node = h('div', 'msg user', e.text || '');
    }
    main.append(node);
  }
  /* "working…" is read off the shape of the stream, not off any state the
   * server keeps: anything that is not the agent's prose means the turn is
   * still going, and the prose is what ends it. Garnish, so it is wrong in
   * the cases a heuristic is wrong (a turn that died mid-tool, a chat left
   * on the operator's last message) — which is exactly why it never
   * touches the composer. Replay lands on the same answer as live append,
   * since each entry sets it and the last one wins. */
  setWorking(!(e.role === 'assistant' && e.kind === 'text'));
  if (stick) main.scrollTop = main.scrollHeight;
}

/* A step is the machinery of a turn rather than a thing said. tool_result
 * blocks arrive under the USER role (that is how the wire carries them), so
 * this reads kind and never role. */
function isStep(e) {
  return e.kind === 'tool_use' || e.kind === 'tool_result' || e.kind === 'thinking';
}

/* growGroup appends a step to the turn's group, opening one if the last
 * thing rendered was prose. Live append and replay go through here
 * identically — the group is just "the open one", so entries streaming in
 * grow the row already on screen instead of starting a new one. */
function growGroup(main, e) {
  if (!view.group) {
    var box = h('details', 'steps');
    var sum = h('summary', '', '');
    var body = h('div', 'steps-body');
    box.append(sum, body);
    main.append(box);
    view.group = { sum: sum, body: body, n: 0, tool: '' };
  }
  var g = view.group;
  g.n++;
  if (e.tool) g.tool = e.tool;
  else if (!g.tool) g.tool = 'thinking';
  g.sum.textContent = '▸ ' + g.n + (g.n === 1 ? ' step' : ' steps') + ' · ' + g.tool;
  g.body.append(stepRow(e));
}

/* One step's own row — collapsed to a headline, expandable to everything.
 * Unchanged in kind from grove-218; what changed is that a tool_use's
 * headline is now the CALL rather than its input JSON. */
function stepRow(e) {
  if (e.kind === 'thinking') {
    var t = h('details', 'tool think');
    t.append(h('summary', '', '… thinking'));
    t.append(h('pre', '', e.text || ''));
    return t;
  }
  var node = h('details', 'tool');
  var use = e.kind === 'tool_use';
  var head = use ? toolSummary(e.tool, e.text) : oneLine(e.text);
  node.append(h('summary', '', (use ? '▸ ' : '◂ ') + (e.tool || 'tool') + ' — ' + head));
  node.append(h('pre', '', use ? toolDetail(e.text) : (e.text || '')));
  return node;
}

/* SALIENT is the one field that IS the call, per tool: for a Bash row the
 * command, not `{"command":"…","description":"…"}`. GENERIC catches the
 * tools not listed — including whatever MCP server the operator wired up
 * this week — by looking for the field names that carry a call's subject.
 * Anything that yields nothing readable falls back to the compact JSON,
 * which is where every row started. */
var SALIENT = {
  Bash: ['command'],
  BashOutput: ['bash_id'],
  Read: ['file_path'],
  Write: ['file_path'],
  Edit: ['file_path'],
  NotebookEdit: ['notebook_path'],
  Glob: ['pattern'],
  Grep: ['pattern'],
  WebFetch: ['url'],
  WebSearch: ['query'],
  Task: ['description'],
  Agent: ['description'],
  Skill: ['skill'],
  SlashCommand: ['command'],
  TodoWrite: ['todos']
};
var GENERIC = ['command', 'file_path', 'notebook_path', 'path', 'pattern',
  'query', 'url', 'description', 'title', 'name', 'skill', 'prompt', 'text'];

function toolSummary(tool, text) {
  var input = parseInput(text);
  if (!input) return oneLine(text);
  var keys = (SALIENT[tool] || []).concat(GENERIC);
  var list = null, listKey = '';
  for (var i = 0; i < keys.length; i++) {
    var v = input[keys[i]];
    if (typeof v === 'string' && v.trim()) return oneLine(v);
    if (typeof v === 'number' || typeof v === 'boolean') return String(v);
    if (!list && Array.isArray(v) && v.length) { list = v; listKey = keys[i]; }
  }
  /* A call whose subject is a LIST (TodoWrite's todos) says how many rather
   * than spilling the array into a one-line summary. */
  if (list) return list.length + ' ' + listKey;
  /* Nothing named — an MCP tool the operator wired up this week, say. Its
   * FIRST string field, named, still beats a braces-and-quotes blob: the
   * transcript preserves the order the model wrote them in, and the first
   * one is nearly always the subject. */
  var own = Object.keys(input);
  for (var j = 0; j < own.length; j++) {
    var w = input[own[j]];
    if (typeof w === 'string' && w.trim()) return oneLine(own[j] + ': ' + w);
  }
  return oneLine(text);
}

/* The expanded row keeps the whole input — a headline is a shortcut, never
 * a substitute. Field-per-line rather than the raw compact JSON, because
 * the field that matters is usually a shell command and reading one back
 * through \n escapes and quote soup is the thing this row exists to fix.
 * Anything that is not a plain object prints exactly as it arrived. */
function toolDetail(text) {
  var input = parseInput(text);
  if (!input) return text || '';
  var keys = Object.keys(input);
  if (!keys.length) return text || '';
  return keys.map(function (k) {
    var v = input[k];
    var s = typeof v === 'string' ? v : JSON.stringify(v, null, 2);
    return s.indexOf('\n') >= 0 ? k + ':\n' + s : k + ': ' + s;
  }).join('\n');
}

function parseInput(text) {
  if (!text) return null;
  var o;
  try { o = JSON.parse(text); } catch (_) { return null; }
  if (!o || typeof o !== 'object' || Array.isArray(o)) return null;
  return o;
}

/* The indicator lives in the composer's own strip and is never a gate: the
 * operator can queue the next message while a turn is still running, which
 * is how the desk works too. */
function setWorking(on) {
  view.working = !!on;
  el('working').hidden = !on;
}

function oneLine(s) {
  s = (s || '').replace(/\s+/g, ' ').trim();
  return s.length > 90 ? s.slice(0, 90) + '…' : s;
}

/* ---------------- composer ---------------- */

function composer(c) {
  var footer = el('footer'), text = el('text'), send = el('send'), resume = el('resume');
  footer.hidden = false;
  el('why').textContent = '';
  resume.hidden = true;

  /* `writable` is the server's answer and the ONLY input to this gate. */
  if (!c.writable) {
    text.disabled = send.disabled = true;
    text.value = '';
    text.placeholder = 'read-only';
    el('why').textContent = c.kind === 'cockpit'
      ? 'this is the cockpit’s own orchestrator pane — someone may be typing in it at the desk, so it is read-only here'
      : 'archived: no live pane. revive it to continue the same conversation.';
    if (c.kind === 'archived' && c.session_id) {
      resume.hidden = false;
      resume.textContent = 'revive this chat';
      resume.onclick = function () {
        resume.classList.add('busy');
        resume.textContent = 'reviving…';
        api('/api/chats/' + encodeURIComponent(addr(c)) + '/resume', {})
          .then(function (j) {
            return loadChats().then(function () {
              location.hash = '#/c/' + encodeURIComponent(j.session || addr(c));
              render();
            });
          })
          .catch(function (e) { resume.classList.remove('busy'); resume.textContent = 'revive this chat'; showError(e); });
      };
    }
    return;
  }

  text.disabled = send.disabled = false;
  text.placeholder = 'message this chat…';
  var submit = function () {
    var body = text.value.trim();
    if (!body) return;
    text.disabled = send.disabled = true;
    send.textContent = 'sending…';
    /* The reply is NOT rendered from this response — it arrives on the
     * stream, out of the transcript, like every other entry. Sending is
     * slow on purpose (bracketed paste, settle, a separate Enter, then a
     * scrape proving it SUBMITTED), so the button says so. */
    api('/api/chats/' + encodeURIComponent(addr(c)) + '/send', { text: body })
      .then(function () {
        text.value = '';
        autosize();
        /* Delivered, so the turn is running — say so now rather than
         * waiting for the agent's first thinking block to land. The
         * stream takes the indicator over from here. */
        setWorking(true);
      })
      .catch(showError)
      .then(function () {
        text.disabled = send.disabled = false;
        send.textContent = 'send';
        text.focus();
      });
  };
  send.onclick = submit;
  text.onkeydown = function (ev) {
    if (ev.key === 'Enter' && !ev.shiftKey && !ev.isComposing) { ev.preventDefault(); submit(); }
  };
  text.oninput = autosize;
  autosize();
}

function autosize() {
  var t = el('text');
  t.style.height = 'auto';
  t.style.height = Math.min(t.scrollHeight, window.innerHeight * 0.4) + 'px';
}

/* renderKeys grows the raw-key row when the pane scrape sees a modal.
 * Detection is garnish by house rule — it reads the pane, and a pane is
 * the one thing here that is not the transcript — so a miss must be
 * survivable: the row says what to do when it is wrong. */
function renderKeys(p) {
  var box = el('keys');
  box.textContent = '';
  if (!p || !p.detected) { document.body.classList.remove('picker'); return; }
  document.body.classList.add('picker');
  box.append(h('div', 'label', p.prompt || 'the chat is asking something — answer with a key'));
  (p.keys || []).forEach(function (k) {
    var b = h('button', '', k);
    b.onclick = function () {
      box.classList.add('busy');
      api('/api/chats/' + encodeURIComponent(view.addr) + '/keys', { key: k })
        .catch(showError)
        .then(function () { box.classList.remove('busy'); });
    };
    box.append(b);
  });
}

/* ---------------- routing ---------------- */

function render() {
  closeStream();
  closeSheet();
  var parts = (location.hash || '#/').slice(1).split('/');
  if (parts[1] === 'w' && parts[2]) screenWorkspace(decodeURIComponent(parts[2]));
  else if (parts[1] === 'c' && parts[2]) screenChat(decodeURIComponent(parts[2]));
  else screenProjects();
  /* Whatever just landed on screen IS the painted state, however it got
   * there (first load, ⟳, a tap, a poll). Recording it here is what keeps
   * repaintList's change check honest from every entry point. */
  poll.sig = listSig();
}

/* ---------------- live lists (grove-258) ---------------- */

/* The open chat is live because SSE pushes it. The LISTS used to be frozen
 * at page load: `busy` dots, ages and the card meta all aged in place until
 * someone tapped ⟳, so opening the phone after any idle period showed a
 * snapshot of whenever it was last opened. The loop below is the fix, and
 * its shape is all about what it REFUSES to do — no fetch while a chat
 * stream is the live view, none while the tab is hidden, and no repaint
 * that the reader would notice when nothing actually changed. */

function isListScreen() {
  var parts = (location.hash || '#/').slice(1).split('/');
  return !(parts[1] === 'c' && parts[2]);
}

/* listSig folds the RENDERED strings, not the raw rows — including each
 * row's `ago()` label. So a row crossing a minute boundary counts as a
 * change and the ages tick on the same 5s beat, with no second timer, while
 * a poll that returns an identical list is a no-op down to the DOM. */
function listSig() {
  if (!isListScreen()) return null;
  var parts = [location.hash || '#/', view.profiles.length, view.loaded ? 1 : 0];
  view.chats.forEach(function (c) {
    parts.push(c.workspace, c.kind, c.session || '', c.session_id || '',
      chatTitle(c), c.busy ? 1 : 0, c.writable ? 1 : 0, ago(activeAt(c)));
  });
  return parts.join('\u0000');
}

/* The list screens rebuild `main` wholesale, so an unguarded re-render every
 * 5s would blink and throw the reader back to the top of a long list. Guard
 * on the signature, and restore the scroll offset for the repaints that do
 * happen — that is what makes a poll of unchanged data visually inert. */
function repaintList() {
  if (listSig() === poll.sig) return;
  var main = el('main');
  var top = main.scrollTop;
  render();
  main.scrollTop = top;
}

/* refreshList is the ONLY thing that fetches on a timer, and it declines in
 * every case where a fetch would be waste or damage: off a list screen (SSE
 * owns that view), hidden tab (nobody is looking, and it is the operator's
 * battery), a fetch already in flight, or the profile sheet open — render()
 * closes the sheet, so a poll underneath an open sheet would dismiss the
 * question the operator is mid-way through answering. */
function refreshList() {
  if (!isListScreen() || document.hidden || poll.inflight) return;
  if (!el('sheet').hidden) return;
  poll.inflight = true;
  /* Re-phase the timer so a hashchange or a refocus and the next tick do
   * not land back to back. */
  if (poll.timer) { clearInterval(poll.timer); poll.timer = setInterval(refreshList, POLL_MS); }
  var done = function () {
    poll.inflight = false;
    /* Failure keeps the last-loaded list on screen — api() has already set
     * the offline class, and a blank list is a worse lie than a stale one.
     * Repainting anyway lets the ages keep ticking while disconnected. */
    repaintList();
  };
  return loadChats().then(done, done);
}

/* The timer exists only while it is allowed to fetch, so a backgrounded tab
 * or an open chat costs literally nothing rather than a guarded wake-up. */
function syncPolling() {
  var want = isListScreen() && !document.hidden;
  if (want && !poll.timer) poll.timer = setInterval(refreshList, POLL_MS);
  else if (!want && poll.timer) { clearInterval(poll.timer); poll.timer = null; }
}

function refresh() {
  /* Profiles ride along with the chat list and never block it: the picker
   * is garnish, the chats are the app. */
  return loadProfiles().then(loadChats).then(render, function (e) { render(); showError(e); });
}

window.addEventListener('hashchange', function () {
  render();
  syncPolling();
  /* Coming back from a chat must not show the list as stale as when it was
   * left — the cached rows paint instantly, then this catches them up. */
  refreshList();
});
/* Unlocking the phone or switching back to the tab is the other moment the
 * list is guaranteed stale, and the one the operator notices most. */
document.addEventListener('visibilitychange', function () {
  syncPolling();
  refreshList();
});
/* Regaining the network refreshes the LISTS. A chat screen is deliberately
 * left alone: re-rendering it wipes the transcript and re-opens the stream
 * from seq 0, which is the full replay the SSE resume exists to avoid
 * (grove-259). Its EventSource reconnects on its own, carrying
 * Last-Event-ID, and onopen clears the offline class when it lands. */
window.addEventListener('online', function () { if (isListScreen()) refresh(); });
window.addEventListener('offline', function () { document.body.classList.add('offline'); });
el('refresh').onclick = refresh;

if (window.marked) window.marked.use({ gfm: true, breaks: true });
render();
refresh();
syncPolling();

/* The service worker caches the shell only (never /api), so a phone that
 * has lost the tailnet opens to "not connected" instead of a blank page.
 * Secure-context only, which is why the deploy needs tailnet HTTPS. */
if ('serviceWorker' in navigator && window.isSecureContext) {
  navigator.serviceWorker.register('sw.js').catch(function () { /* offline shell is a nicety, never a requirement */ });
}
