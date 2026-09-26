/* gv chat — the phone client (grove-218). Hand-written, no toolchain.
 *
 * Three screens, routed on the hash so the phone's back button works:
 *   #/            home: every live chat, history behind a disclosure
 *   #/w/<label>   one project's chats, its history open (a deep link)
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
 * rendered was prose (grove-261); `working` is the stream heuristic's
 * guess at the turn. Both are pure view state — nothing on the wire knows
 * they exist. `turn` is the server's pane read (grove-300's `turn` event)
 * and `turnHold` the moment until which it is too old to trust. `day` is
 * the local calendar day of the last prose entry that carried a time
 * (grove-303) — what decides whether the next one needs a separator.
 * grove-334: `workspaces` is every registered workspace, so home keeps a
 * block (and its `+ new chat`) for one with no chats; `picker` is the open
 * chat's last picker event; `stopped` is "the operator ended this turn" —
 * a stop that landed, or the transcript's interrupt notice — so an idle
 * pane after it reads as stopped, not as "no reply". */
var view = { chats: [], workspaces: [], picker: null, stopped: false, profiles: [], version: '', staleSeen: '', loaded: false, es: null, maxSeq: 0, addr: null, group: null, working: false, pending: [], turn: null, turnHold: 0, day: '', hist: {} };
/* Everything the live-list loop needs: the interval handle (null means the
 * loop is deliberately stopped), a one-flight guard so a poll and a
 * refocus cannot stack fetches, and the signature of what is currently
 * painted. */
var poll = { timer: null, inflight: false, sig: null, at: 0 };
/* The list stream (grove-307): the server pushes /api/chats on change, so
 * while it is `healthy` the poll above stays stopped (poll.timer null) and
 * is only the fallback. `ages` is a repaint-only beat — no fetch — that
 * keeps the rows' "5m ago" labels moving while the list itself is quiet. */
var feed = { es: null, healthy: false, ages: null };
var AGES_MS = 30000;
/* The last few chats left, kept rendered (grove-297): without this, every
 * trip back into a chat replayed its whole transcript from seq 0 — a long
 * orchestrator chat re-parsed hundreds of markdown blocks and scrolled the
 * reader through all of it. Newest last; CHAT_CACHE bounds the DOM held
 * off screen. */
var chatCache = [];
var CHAT_CACHE = 3;
var POLL_MS = 5000;
/* With notifications on, the list keeps being read while nobody is looking
 * at it (tab hidden, or a chat open) so a chat going `waiting` elsewhere
 * can alert (grove-302). Slower than the on-screen beat: it is the
 * operator's battery, and a question keeps. */
var WATCH_MS = 15000;

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
    noteRows();
    return view.chats;
  });
}

/* The registered workspaces (grove-334). Garnish like profiles: a failed
 * read keeps what home already had, and the chat rows still name theirs. */
function loadWorkspaces() {
  return api('/api/workspaces').then(function (j) {
    view.workspaces = j.workspaces || [];
  }, function () { /* keep the last list */ });
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

/* The server's build (grove-286), learned once — from /api/version or the
 * list stream's first event, whichever lands first — and shown dim at the
 * foot of home. A running server keeps serving the binary it loaded, so
 * after a `gv update` without a service restart the phone is the one place
 * the staleness shows; and once the service IS restarted, a later read
 * that disagrees is a page talking to a newer server than it was built
 * against. Garnish: a failed read shows nothing. */
function loadVersion() {
  return api('/api/version').then(function (j) { noteVersion(j.version); }, function () { /* garnish */ });
}

function noteVersion(v) {
  if (typeof v !== 'string' || !v) return;
  if (!view.version) {
    view.version = v;
    repaintIdle();
    return;
  }
  if (v === view.version || v === view.staleSeen) return;
  view.staleSeen = v;
  showToast('server updated — reload');
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

/* The toast: one at a time, newest replaces (grove-298). It never lives
 * inside #main — see index.html's note on #toast — so it survives the
 * wholesale re-renders that would otherwise wipe a prepended error before
 * its timeout was up, and stays visible without scrolling on both the
 * chat screen (above the composer) and the list screens (footer is
 * hidden there, so the same bottom slot serves as "above the list"). */
var toastTimer = null;

function showToast(text) {
  var box = el('toast');
  box.textContent = text;
  box.hidden = false;
  restartToastTimer();
}

function restartToastTimer() {
  clearTimeout(toastTimer);
  toastTimer = setTimeout(hideToast, 6000);
}

function hideToast() {
  clearTimeout(toastTimer);
  toastTimer = null;
  el('toast').hidden = true;
}

/* fault SSE events and every api() failure both land here, and the text is
 * always the server's own — never invented in this file (the house rule
 * at the top of the file). */
function showError(e) {
  showToast(String(e && e.message ? e.message : e));
}

function setHeader(title, sub, back) {
  el('title').textContent = title;
  el('subtitle').textContent = sub;
  el('back').hidden = !back;
  el('back').onclick = back || null;
  el('end').hidden = true;
}

/* ---------------- screen 1: home — the live chats (grove-302) ---------------- */

/* The home screen answers "which chat do I open?" in one tap. It used to be
 * a projects list that only carried counts (`2 live · 14 other`), so the
 * chat you actually use was three taps away. Now every live row (kind chat,
 * plus the cockpit's read-only pane) across every workspace is on screen,
 * and the past sits behind a per-workspace `history (N)` disclosure.
 *
 * "archived" is the contract's word (kind: "archived"); "history" is the
 * page's. Every string the operator reads says history. */

/* A live chat quiet for longer than this offers to be ended: it is holding
 * a Claude process (and its memory) that nobody has spoken to in hours. */
var IDLE_HINT_S = 3 * 3600;
var HISTORY_EXPLAIN = 'history = conversations with no running Claude process; tap one to revive it.';

/* liveOrder is the home screen's whole order rule, and the only one this
 * file has: needs you → running → most recently active. It deliberately
 * spans workspaces — a question blocking an agent in unbrewed outranks a
 * quiet chat in grove, whatever their labels. The server's `ls` order
 * (chat.Less: workspace, kind, recency, number) is what ties fall back to,
 * because the sort is stable. */
function liveOrder(a, b) {
  var ra = rank(a), rb = rank(b);
  if (ra !== rb) return ra - rb;
  return new Date(activeAt(b)).getTime() - new Date(activeAt(a)).getTime();
}

/* rank: needs you (0) → working (1) → the rest (2). `turn` (grove-334) is
 * the row's pane read; a server that predates it sends none, and `busy`
 * (a live process) stands in for working as it always did. */
function rank(c) {
  if (needsYou(c)) return 0;
  return turnWord(c) === 'working' ? 1 : 2;
}

function needsYou(c) { return !!c.waiting || c.turn === 'waiting'; }

/* turnWord is what a live chat row is doing, in the page's words: a turn
 * that is running is `working`, a claude at its prompt `idle`, no claude
 * in the pane `stopped`. `busy` only ever meant "a process is alive", so
 * it is the fallback for an unread turn, never the answer. */
function turnWord(c) {
  switch (c.turn) {
    case 'running': return 'working';
    case 'idle': return 'idle';
    case 'stopped': return 'stopped';
    case 'errored': return 'errored';
    case 'waiting': return 'needs you';
  }
  if (c.turn === undefined) return c.busy ? 'working' : 'stopped';
  return c.busy ? 'live' : 'stopped';
}

/* The kind as the operator reads it — the badge and the chat subtitle. */
function kindWord(c) { return c.kind === 'archived' ? 'history' : c.kind; }

/* idleFor is the `idle 5h · end?` label, or '' when the row gets no hint:
 * only a live kind-chat row can be ended, and one sitting on a question is
 * not idle, it is waiting on the operator. Same rounding as ago(), so the
 * list signature (which folds ago) moves whenever this label does. */
function idleFor(c) {
  if (c.kind !== 'chat' || needsYou(c)) return '';
  var s = (Date.now() - new Date(activeAt(c)).getTime()) / 1000;
  if (!(s > IDLE_HINT_S)) return '';
  return 'idle ' + (s < 172800 ? Math.round(s / 3600) + 'h' : Math.round(s / 86400) + 'd') + ' · end?';
}

function stateBadge(c) {
  if (c.kind === 'archived') return h('span', 'badge history', 'history');
  if (needsYou(c)) return h('span', 'badge waiting', 'needs you');
  if (c.kind === 'cockpit') return h('span', 'badge cockpit', 'cockpit');
  var w = turnWord(c);
  return h('span', 'badge chat ' + w, w);
}

/* chatRow is one tappable chat, on every list. showWs tags it with its
 * workspace — on the home screen, where rows from different workspaces sit
 * together; not where the workspace is already the heading. */
function chatRow(c, showWs) {
  var b = h('button', 'row' + (needsYou(c) ? ' waiting' : ''));
  b.append(h('div', 'title', chatTitle(c)));
  var meta = h('div', 'meta');
  if (showWs) meta.append(h('span', 'ws', c.workspace));
  meta.append(stateBadge(c));
  if (c.kind === 'cockpit' ? c.busy : turnWord(c) === 'working') meta.append(h('span', 'dot'));
  var idle = idleFor(c);
  if (!idle) meta.append(h('span', '', ago(activeAt(c))));
  if (c.kind === 'cockpit') meta.append(h('span', '', 'read-only'));
  if (!c.session_id) meta.append(h('span', '', 'unidentified'));
  if (idle) {
    /* grove-294's End-chat sheet, reached from the row: it explains what
     * ending keeps before it does anything. */
    var end = h('span', 'idle', idle);
    end.setAttribute('role', 'button');
    end.onclick = function (ev) { ev.stopPropagation(); openEndSheet(c); };
    meta.append(end);
  }
  b.append(meta);
  b.onclick = function () { location.hash = '#/c/' + encodeURIComponent(addr(c)); };
  return b;
}

/* newChat is `+ new chat`, wherever it is tapped. The choice is always
 * SHOWN, never inferred — the desk's own rule (grove-105). grove-293: the
 * sheet's rows come from the workspace itself, each naming the model it
 * will run, and there are always Claude tiers to pick from. If that list
 * cannot be read, the pre-293 behavior stands: the profile sheet when
 * profiles exist, else straight to the host default — a broken model list
 * must never be a broken new-chat button. */
function newChat(label, add) {
  api('/api/workspaces/' + encodeURIComponent(label) + '/models').then(function (j) {
    var opts = j.models || [];
    if (opts.length) return openModelSheet(label, add, opts);
    if (view.profiles.length) return openProfileSheet(label, add);
    spawnChat(label, '', '', add);
  }, function () {
    if (view.profiles.length) return openProfileSheet(label, add);
    spawnChat(label, '', '', add);
  });
}

/* One workspace's footer on the home screen: its name, `+ new chat`, and
 * its history behind a disclosure that expands INLINE (Dean, 2026-09-25) —
 * open state is per workspace and survives the 5s poll's repaints.
 * forceOpen is the #/w/<label> deep link, which exists to show history. */
function workspaceBlock(label, rows, forceOpen) {
  var sec = h('section', 'ws-block');
  var head = h('div', 'ws-head');
  head.append(h('span', 'ws-name', label));
  var add = h('button', 'ws-new', '+ new chat');
  add.setAttribute('aria-label', 'new chat in ' + label);
  add.onclick = function () { newChat(label, add); };
  head.append(add);
  sec.append(head);
  var past = rows.filter(function (c) { return c.kind === 'archived'; });
  if (!past.length) return sec;
  var open = forceOpen || !!view.hist[label];
  var t = h('button', 'disclosure', (open ? '▾ ' : '▸ ') + 'history (' + past.length + ')');
  t.setAttribute('aria-expanded', open ? 'true' : 'false');
  if (forceOpen) t.disabled = true;
  else t.onclick = function () {
    view.hist[label] = !open;
    var main = el('main'), top = main.scrollTop;
    render();
    main.scrollTop = top;
  };
  sec.append(t);
  if (open) {
    sec.append(h('div', 'explain', HISTORY_EXPLAIN));
    past.forEach(function (c) { sec.append(chatRow(c, false)); });
  }
  return sec;
}

function byWorkspace() {
  var labels = [], by = {};
  view.workspaces.forEach(function (l) {
    if (!by[l]) { by[l] = []; labels.push(l); }
  });
  view.chats.forEach(function (c) {
    if (!by[c.workspace]) { by[c.workspace] = []; labels.push(c.workspace); }
    by[c.workspace].push(c);
  });
  labels.sort();
  return { labels: labels, by: by };
}

function screenHome() {
  setHeader('gv chat', 'live chats', null);
  el('footer').hidden = true;
  var main = el('main');
  main.textContent = '';
  var g = byWorkspace();
  if (!g.labels.length) {
    main.append(h('div', 'empty', view.loaded
      ? 'no registered workspaces on this machine — run gv init in a repo to add one'
      : 'loading…'));
    if (view.version) main.append(h('div', 'ver', 'gv ' + view.version));
    return;
  }
  var live = view.chats.filter(function (c) { return c.kind !== 'archived'; }).sort(liveOrder);
  if (live.length) live.forEach(function (c) { main.append(chatRow(c, true)); });
  else main.append(h('div', 'empty', 'no live chats — start one below, or revive one from history'));
  g.labels.forEach(function (label) { main.append(workspaceBlock(label, g.by[label], false)); });
  if (view.version) main.append(h('div', 'ver', 'gv ' + view.version));
}

/* ---------------- #/w/<label>: one workspace ---------------- */

/* Kept as a deep link (grove-302): the same rows as home, one workspace,
 * with its history already open. */
function screenWorkspace(label) {
  setHeader(label, 'chats in this project', function () { location.hash = '#/'; });
  el('footer').hidden = true;
  var main = el('main');
  main.textContent = '';
  var rows = view.chats.filter(function (c) { return c.workspace === label; });
  rows.filter(function (c) { return c.kind !== 'archived'; }).sort(liveOrder)
    .forEach(function (c) { main.append(chatRow(c, false)); });
  main.append(workspaceBlock(label, rows, true));
  if (!rows.length) main.append(h('div', 'empty', 'no chats here yet'));
}

/* spawnChat is the one place `+ new chat` reaches the server, whichever
 * row was tapped. An empty profile or model sends NO key at all, so the
 * request is byte-identical to the one grove-218 sent and the host spawns
 * on its own Claude. */
function spawnChat(label, profile, model, add) {
  add.classList.add('busy');
  (add.querySelector('.title') || add).textContent = 'starting a chat…';
  var body = {};
  if (profile) body.profile = profile;
  if (model) body.model = model;
  return api('/api/workspaces/' + encodeURIComponent(label) + '/new', body)
    .then(function (j) {
      if (j.session) forgetChat(j.session);
      return loadChats().then(function () {
        if (j.session) location.hash = '#/c/' + encodeURIComponent(j.session);
        else render();
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
    return function () { closeSheet(); spawnChat(label, profile, '', add); };
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

/* tierName: "opus" → "Opus" for a built-in tier; an operator's own
 * orchestrator.models entry (a full model id) is shown as written. */
function tierName(m) {
  return /^(opus|sonnet|haiku)$/.test(m) ? m.charAt(0).toUpperCase() + m.slice(1) : m;
}

/* The new-chat sheet (grove-293): ONE sheet, rows in the server's order —
 * the host default, the Claude tiers, then each model profile. Every row's
 * meta line is the server's `runs`: the model that spawn will actually run
 * ("account default" when nothing on the host names one). The page never
 * resolves a model itself; it renders what it was told and POSTs back the
 * row's {profile, model} unchanged. */
function openModelSheet(label, add, opts) {
  var panel = el('sheet-panel');
  panel.textContent = '';
  panel.append(h('div', 'sheet-title', 'new chat in ' + label + ' — on which model?'));
  opts.forEach(function (o) {
    var b = h('button', 'row');
    var title = o.profile ? o.profile : o.model ? 'Claude · ' + tierName(o.model) : 'Claude (host default)';
    var kind = o.profile ? 'model profile' : o.model ? 'Claude tier' : 'the operator’s own Claude';
    b.append(h('div', 'title', title));
    b.append(h('div', 'meta', 'runs ' + (o.runs || 'account default') + ' · ' + kind));
    b.onclick = function () { closeSheet(); spawnChat(label, o.profile || '', o.model || '', add); };
    panel.append(b);
  });
  var cancel = h('button', 'cancel', 'cancel');
  cancel.onclick = closeSheet;
  panel.append(cancel);
  var sheet = el('sheet');
  sheet.onclick = function (ev) { if (ev.target === sheet) closeSheet(); };
  sheet.hidden = false;
}

function closeSheet() { el('sheet').hidden = true; }

/* grove-294: End chat. The sheet EXPLAINS before it acts — the operator
 * could not tell what "archived" meant or how to stop a chat eating
 * memory, so the words say exactly what ending does and does not lose.
 * Only a live kind-chat row gets here; the server refuses anything else in
 * the CLI's own words either way. After ending, home: the row is history
 * now, revivable from there. */
function openEndSheet(c) {
  var panel = el('sheet-panel');
  panel.textContent = '';
  panel.append(h('div', 'sheet-title', 'end ' + chatTitle(c) + '?'));
  panel.append(h('div', 'sheet-body',
    'Ends the Claude process running this chat (frees its memory and stops any work in progress). ' +
    'The conversation is kept in history, and you can revive it later.'));
  /* The turn state, not the stream heuristic (grove-334): `working` can be
   * a stale guess on a chat whose pane has long since died. The open
   * chat's own pane read is freshest; the row's is the fallback. */
  var t = view.addr === addr(c) && view.turn ? view.turn.state : c.turn;
  if (t === 'running') {
    panel.append(h('div', 'sheet-body', 'It is working right now; ending stops that turn.'));
  }
  var go = h('button', 'row danger');
  go.append(h('div', 'title', 'End chat'));
  go.append(h('div', 'meta', c.session || ''));
  go.onclick = function () {
    go.classList.add('busy');
    go.querySelector('.title').textContent = 'ending…';
    api('/api/chats/' + encodeURIComponent(addr(c)) + '/close', {})
      .then(function () {
        closeSheet();
        closeStream();
        return loadChats().then(function () {
          if (location.hash === '#/' || location.hash === '') render();
          else location.hash = '#/';
        });
      })
      .catch(function (e) { closeSheet(); showError(e); });
  };
  panel.append(go);
  var cancel = h('button', 'cancel', 'cancel');
  cancel.onclick = closeSheet;
  panel.append(cancel);
  var sheet = el('sheet');
  sheet.onclick = function (ev) { if (ev.target === sheet) closeSheet(); };
  sheet.hidden = false;
}

/* ---------------- screen 3: one chat ---------------- */

function screenChat(a) {
  var c = chatByAddr(a);
  /* Clear any half-typed draft on the way in. A composer that carries text
   * from the chat you just left is a message delivered to the wrong agent
   * one tap later — the exact failure the rest of this subsystem refuses
   * to risk (grove-116/78). */
  if (view.addr !== a) el('text').value = loadDraft(a);
  view.addr = a;
  /* Pending bubbles belong to the transcript being wiped below; a message
   * that did land comes back on the replay like any other entry. A cached
   * transcript brings its own back further down. */
  view.pending = [];
  /* Back is always home (grove-302): the live list is one tap from every
   * chat, including one opened cold from a notification. */
  var back = function () { location.hash = '#/'; };
  /* grove-293: the model the chat was spawned to run (its pane tag), when
   * grove tagged one — the same name its new-chat sheet row showed. */
  setHeader(c ? chatTitle(c) : a, c ? c.workspace + ' · ' + kindWord(c) + (c.model ? ' · ' + c.model : '') : 'chat', back);
  if (c && c.kind === 'chat') {
    el('end').hidden = false;
    el('end').onclick = function () { openEndSheet(c); };
  }
  var main = el('main');
  main.textContent = '';
  /* A fresh transcript starts with no open group and nothing running —
   * the stream that is about to replay decides both. */
  view.group = null;
  view.day = '';
  view.turn = null;
  view.stopped = false;
  setWorking(false);
  el('jump').hidden = true;
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
  var kept = takeChat(a);
  /* Same address, different conversation (restarted at the desk, say):
   * resuming at the old seq would silently skip the new one's start. */
  if (kept && kept.sid !== c.session_id) kept = null;
  if (kept) {
    /* Re-attach what was rendered and ask only for what landed since. The
     * stream's first picker and turn events are fresh reads, so the cached
     * `turn` is dropped rather than trusted (turnHold, a send's own clock,
     * rides along). The seq guard makes any overlap harmless. */
    main.append(kept.node);
    view.maxSeq = kept.maxSeq;
    view.group = kept.group;
    view.day = kept.day;
    view.pending = kept.pending;
    /* A separator painted as "today" before midnight is "yesterday" now;
     * relabel so the restore reads the same as a fresh replay would. */
    kept.node.querySelectorAll('.day').forEach(function (d) { d.textContent = dayLabel(d.dataset.day); });
    view.turnHold = kept.turnHold;
    view.stopped = kept.stopped;
    setWorking(kept.working);
    main.scrollTop = kept.top;
    el('jump').hidden = kept.jump;
    openStream(a, view.maxSeq);
    return;
  }
  view.maxSeq = 0;
  view.turnHold = 0;
  openStream(a, 0);
}

/* keepChat moves the open chat's rendered transcript off screen along with
 * every piece of view state that describes it, so re-opening it is a
 * re-attach plus a `?since=` stream rather than a replay. Only a chat with
 * a live stream is kept: the no-session and not-in-list screens are one
 * line each and have nothing to resume. */
function keepChat() {
  if (!view.es || !view.addr) return;
  var main = el('main');
  var entry = {
    addr: view.addr, sid: (chatByAddr(view.addr) || {}).session_id,
    maxSeq: view.maxSeq, group: view.group, day: view.day, working: view.working,
    pending: view.pending, turnHold: view.turnHold, stopped: view.stopped,
    top: main.scrollTop, jump: el('jump').hidden, node: document.createDocumentFragment(),
  };
  /* Moving the nodes (not cloning them) keeps `group` and the pending
   * bubbles pointing at the very elements they update. */
  while (main.firstChild) entry.node.append(main.firstChild);
  forgetChat(view.addr);
  chatCache.push(entry);
  if (chatCache.length > CHAT_CACHE) chatCache.shift();
}

function takeChat(a) {
  for (var i = 0; i < chatCache.length; i++) {
    if (chatCache[i].addr === a) return chatCache.splice(i, 1)[0];
  }
  return null;
}

/* forgetChat drops a kept transcript that no longer describes the chat at
 * that address: a revive or a spawn starts a new conversation there. */
function forgetChat(a) { takeChat(a); }

/* openStream replays the transcript and then follows it. Replay and live
 * append come down ONE stream, so there is no seam to get wrong. The
 * reconnect a phone waking up or a tailnet blip forces is the browser's
 * own: every entry arrives stamped `id: <seq>`, so EventSource replays that
 * back as Last-Event-ID and the server resumes past it (grove-259) — no URL
 * to rewrite for that. `since` is the other entry point: a chat re-opened
 * from the cache resumes past what it kept (grove-297). The seq check
 * below makes a duplicate replay harmless either way. */
function openStream(a, since) {
  closeStream();
  var es = new EventSource('/api/chats/' + encodeURIComponent(a) + '/events' + (since ? '?since=' + since : ''));
  view.es = es;
  es.addEventListener('entry', function (ev) {
    var e;
    try { e = JSON.parse(ev.data); } catch (_) { return; }
    if (e.seq <= view.maxSeq) return;
    view.maxSeq = e.seq;
    settleReplay();
    appendEntry(e);
  });
  es.addEventListener('picker', function (ev) {
    var p;
    try { p = JSON.parse(ev.data); } catch (_) { return; }
    view.picker = p;
    renderKeys(p);
    notePicker(a, p);
  });
  es.addEventListener('turn', function (ev) {
    var t;
    try { t = JSON.parse(ev.data); } catch (_) { return; }
    view.turn = t;
    renderWorking();
    /* A modal the picker cannot read shows only here (grove-334). */
    renderKeys(view.picker);
  });
  es.addEventListener('fault', function (ev) {
    var msg = '';
    try { msg = JSON.parse(ev.data); } catch (_) { return; }
    showError(new Error(msg));
  });
  es.onerror = function () { document.body.classList.add('offline'); };
  es.onopen = function () { document.body.classList.remove('offline'); settleReplay(); };
}

function closeStream() {
  if (view.es) { view.es.close(); view.es = null; }
  clearTimeout(alerts.settle);
  alerts.live = false;
  alerts.endSeq = 0;
  /* Only the open chat is streamed, so only the open chat is known to be
   * waiting; leaving it is the operator having seen it. */
  if (view.addr) { delete alerts.waiting[view.addr]; delete alerts.picked[view.addr]; }
  syncBadge();
  document.body.classList.remove('picker');
  el('keys').textContent = '';
  view.picker = null;
  syncSend();
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
  if (e.kind === 'meta') {
    /* A harness wrapper (grove-315): a slash command, a `!` escape, a
     * background task's notice. Not prose, so it neither closes the turn's
     * group nor says anything about whether the agent is working. */
    (view.group ? view.group.body : main).append(metaChip(e));
    /* grove-334: an interrupt is the one wrapper that says something about
     * the turn — it ENDED it. Idle after it is stopped, not "no reply". */
    if (e.tool === 'interrupt') { view.stopped = true; setWorking(false); }
    if (stick) main.scrollTop = main.scrollHeight;
    else el('jump').hidden = false;
    return;
  }
  if (isStep(e)) {
    growGroup(main, e);
  } else {
    /* Prose — the operator's or the agent's — closes the group; the next
     * step opens a new one. That is the whole grouping rule, and it needs
     * nothing from the server: a turn's boundary is already visible in the
     * shape of the stream. */
    view.group = null;
    stampDay(main, e.ts);
    var node;
    if (e.role === 'assistant') {
      node = h('div', 'msg assistant');
      /* Safe because of the server's Content-Security-Policy, not because
       * marked sanitizes (it does not, since v8). See index.html's note. */
      node.innerHTML = window.marked ? window.marked.parse(e.text || '') : '';
      if (!window.marked) node.textContent = e.text || '';
    } else {
      settlePending(e.text);
      /* The operator spoke again: a new turn, no longer a stopped one. */
      view.stopped = false;
      wantLabel();
      node = h('div', 'msg user', e.text || '');
    }
    var at = clock(e.ts);
    if (at) node.append(h('div', 'at', at));
    main.append(node);
  }
  /* "working…" is read off the shape of the stream, not off any state the
   * server keeps: anything that is not the agent's prose means the turn is
   * still going, and the prose is what ends it. Garnish, so it is wrong in
   * the cases a heuristic is wrong (a turn that died mid-tool, a chat left
   * on the operator's last message) — the server's `turn` event overrules
   * it there (renderWorking, grove-300) — and it never touches the
   * composer. Replay lands on the same answer as live append,
   * since each entry sets it and the last one wins. */
  setWorking(!(e.role === 'assistant' && e.kind === 'text'));
  /* Sticky readers follow the bottom silently, as always. A reader who has
   * scrolled away gets the pill instead of a silent append below their
   * view (grove-304) — the whole reason `stick` existed was to know when
   * to leave them alone; this is the other half, telling them something
   * landed while they were. */
  if (stick) main.scrollTop = main.scrollHeight;
  else el('jump').hidden = false;
}

/* ---------------- entry times (grove-303) ----------------
 * Local time, from the browser: `ts` is RFC 3339 on the wire and null when
 * the transcript line had none. A null renders no time and leaves `day`
 * alone, so it can neither open a separator nor break the run of the ones
 * around it. Only prose calls in here — a turn's steps sit inside its
 * group and get no separator or time of their own. */

/* dayKey is the local calendar day as a sortable-enough key, '' for none. */
function dayKey(ts) {
  if (!ts) return '';
  var d = new Date(ts);
  if (isNaN(d)) return '';
  return d.getFullYear() + '-' + (d.getMonth() + 1) + '-' + d.getDate();
}

/* stampDay appends a separator when this prose entry's day differs from
 * the last one rendered — including the first, so the top of a transcript
 * says when it started. Replay, live append and a cache restore all get
 * here with `view.day` describing what is already on screen. */
function stampDay(main, ts) {
  var k = dayKey(ts);
  if (!k || k === view.day) return;
  view.day = k;
  var sep = h('div', 'day', dayLabel(k));
  sep.dataset.day = k;
  main.append(sep);
}

var MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
function dayLabel(k) {
  var p = k.split('-').map(Number);
  var now = new Date();
  var yest = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1);
  var label;
  if (k === dayKey(now)) label = 'today';
  else if (k === dayKey(yest)) label = 'yesterday';
  else label = p[2] + ' ' + MONTHS[p[1] - 1] + (p[0] === now.getFullYear() ? '' : ' ' + p[0]);
  return '— ' + label + ' —';
}

/* clock is the entry's local HH:MM, '' when it has no usable time. */
function clock(ts) {
  if (!ts) return '';
  var d = new Date(ts);
  if (isNaN(d)) return '';
  var two = function (n) { return (n < 10 ? '0' : '') + n; };
  return two(d.getHours()) + ':' + two(d.getMinutes());
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

/* metaChip is a meta entry's row: a small dim chip naming the wrapper,
 * expandable to its text. */
var META_GLYPH = { interrupt: '⏹', command: '⌘', 'task-notification': '⚙', bash: '$', 'bash-output': '◂', 'local-stdout': '◂' };
function metaChip(e) {
  var node = h('details', 'tool meta');
  var head = oneLine(e.text) || (e.tool === 'task-notification' ? 'background task finished' : e.tool);
  node.append(h('summary', '', (META_GLYPH[e.tool] || '·') + ' ' + head));
  node.append(h('pre', '', e.text || ''));
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
  renderWorking();
}

/* renderWorking reconciles the stream heuristic with the server's pane
 * read (grove-300). The heuristic alone lies forever when a message is
 * never answered or a turn dies mid-tool — nothing more ever lands in the
 * transcript to correct it. The `turn` event is what ends those lies:
 *
 *   running           — the spinner is up: working…, whatever the stream says
 *   idle / stopped    — nobody is answering: say so, if we claimed working
 *   errored           — the turn died: say so, with the pane's error line
 *   waiting           — a modal holds the turn; the picker row speaks
 *   unknown / nothing — keep the heuristic
 *
 * A read older than the last send is stale (the pane has not caught up
 * yet), so it is ignored until turnHold passes. Garnish still: none of
 * this touches the composer, and none of it means "done" (grove-205). */
var TURN_HOLD = 8000;
function renderWorking() {
  var t = view.turn && Date.now() >= view.turnHold ? view.turn : null;
  var state = t ? t.state : '';
  var on = view.working, note = '';
  if (state === 'running') on = true;
  else if (state === 'waiting') on = false;
  /* A stopped turn is over (grove-334): whatever the heuristic re-armed on
   * after the Esc, an idle pane now is the stop working, not a lie. */
  else if (view.stopped) on = false;
  else if (on && state === 'errored') note = 'turn errored' + (t.line ? ' — ' + t.line : '');
  else if (on && state === 'idle') note = 'no reply — the pane may have stopped';
  else if (on && state === 'stopped') note = 'no reply — the pane has stopped';
  var w = el('working');
  /* The edge of what is SHOWN is the turn end (grove-305): the heuristic
   * and the pane read have already been reconciled above. A modal is not
   * an end — the picker alerts for that itself. */
  if (!w.hidden && !on && state !== 'waiting') onTurnEnd();
  w.hidden = !on;
  w.classList.toggle('fault', !!note);
  el('wtext').textContent = note || 'working…';
}

/* A fresh send or stop makes the last pane read stale: ignore it until the
 * pane has had time to show the new turn (or its absence). */
function holdTurn() {
  view.turnHold = Date.now() + TURN_HOLD;
  setTimeout(function () { renderWorking(); renderKeys(view.picker); }, TURN_HOLD + 50);
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
  /* `stop` reuses the same server answer as the composer gate below — a
   * read-only row gets no stop button even mid-"working", since the server
   * would 4xx the /keys POST anyway (grove-299). */
  el('stop').hidden = !c.writable;

  /* `writable` is the server's answer and the ONLY input to this gate. */
  if (!c.writable) {
    text.disabled = send.disabled = true;
    text.value = '';
    text.placeholder = 'read-only';
    el('why').textContent = c.kind === 'cockpit'
      ? 'this is the cockpit’s own orchestrator pane — someone may be typing in it at the desk, so it is read-only here'
      : 'history: no running Claude process. revive it to continue the same conversation.';
    if (c.kind === 'archived' && c.session_id) {
      resume.hidden = false;
      resume.textContent = 'revive this chat';
      resume.onclick = function () {
        resume.classList.add('busy');
        resume.textContent = 'reviving…';
        api('/api/chats/' + encodeURIComponent(addr(c)) + '/resume', {})
          .then(function (j) {
            /* A revived chat is a new session: neither the history
             * transcript on screen nor any kept copy may be resumed into
             * it. Closing the stream first is what stops render() from
             * keeping the one on screen. */
            closeStream();
            forgetChat(addr(c));
            if (j.session) forgetChat(j.session);
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
  syncSend();
  var submit = function () {
    var body = text.value.trim();
    if (!body || blocked()) return;
    /* The composer is free the moment the bubble shows: the pending
     * bubble, not a disabled box, is what says a send is in flight. */
    text.value = '';
    autosize();
    /* The draft holds this message until the send succeeds, so a failed
     * send survives a reload too. */
    clearTimeout(draftTimer);
    saveDraft(addr(c), body);
    sendPending(c, body);
    text.focus();
  };
  send.onclick = submit;
  /* Gboard's Enter on a <textarea> is a plain Enter — there is no Shift on
   * a touch keyboard — so Enter-sends would make multi-line messages
   * untypeable. Coarse pointer (touch) gets a newline on Enter and sends
   * only via the button; a fine pointer (desktop) keeps Enter-sends. */
  if (isTouch()) {
    text.enterKeyHint = 'enter';
    text.onkeydown = null;
  } else {
    text.enterKeyHint = 'send';
    text.onkeydown = function (ev) {
      if (ev.key === 'Enter' && !ev.shiftKey && !ev.isComposing) { ev.preventDefault(); submit(); }
    };
  }
  text.oninput = function () {
    autosize();
    /* Address and value are captured NOW, so a save still pending when
     * the operator backs out can only ever land under the chat it was
     * typed in. */
    var a = addr(c), v = text.value;
    clearTimeout(draftTimer);
    draftTimer = setTimeout(function () { saveDraft(a, v); }, 400);
  };
  autosize();
}

/* sendPending shows the operator's message at once as a dimmed bubble
 * (grove-316): on a slow tailnet the verified-submit relay — bracketed
 * paste, settle, a separate Enter, then a scrape proving it SUBMITTED —
 * takes seconds, and the real entry takes longer still to come back out
 * of the transcript. The bubble is a placeholder, never the record: the
 * transcript's own `user` entry replaces it when it lands (settlePending),
 * so the message never shows twice. */
function sendPending(c, body) {
  var node = h('div', 'msg user pending', body);
  var status = h('div', 'status', 'sending…');
  node.append(status);
  var p = { norm: normText(body), node: node };
  view.pending.push(p);
  var main = el('main');
  main.append(node);
  main.scrollTop = main.scrollHeight;
  var a = addr(c);
  api('/api/chats/' + encodeURIComponent(a) + '/send', { text: body })
    .then(function (j) {
      /* grove-333: a submit with no sign of uptake is not a clean ✓ —
       * the relay said so, and the phone repeats it. */
      status.textContent = j && j.warning ? '⚠ sent — no sign it landed yet, check the chat' : 'sent ✓';
      /* Only the draft that WAS this message goes; one typed since stays. */
      if (loadDraft(a).trim() === body) saveDraft(a, '');
      /* Delivered, so the turn is running — say so now rather than
       * waiting for the agent's first thinking block to land. The
       * stream takes the indicator over from here. */
      if (view.es && view.addr === a) {
        view.stopped = false;
        holdTurn();
        setWorking(true);
      } else {
        /* The reader left before the relay answered: mark the kept copy,
         * so re-opening it does not claim nothing is running. */
        var kept = chatCache.filter(function (k) { return k.addr === a; })[0];
        if (kept) { kept.working = true; kept.stopped = false; kept.turnHold = Date.now() + TURN_HOLD; }
      }
      /* A transcript that never echoes it back is not an error — the
       * relay already proved the submit. After a minute the bubble just
       * stops looking in-flight; it still matches if the entry arrives. */
      setTimeout(function () { node.classList.remove('pending'); }, 60000);
    }, function (e) {
      dropPending(p);
      node.classList.remove('pending');
      node.classList.add('failed');
      status.textContent = 'failed — tap to retry';
      node.onclick = function () {
        node.remove();
        if (view.addr === a) sendPending(c, body);
      };
      showError(e);
    });
}

/* settlePending removes the pending bubble a transcript `user` entry
 * stands for. Text is compared whitespace-normalized; a paste can come
 * back with its line endings or trailing space reshaped. Failing an exact
 * match, the oldest bubble whose text is CONTAINED in the entry's (tags
 * stripped) settles: the transcript may store a bracketed paste wrapped
 * in a <pasted_content …> tag, and an unmatched bubble would sit beside
 * the real entry forever. */
function settlePending(text) {
  var n = normText(text), i;
  for (i = 0; i < view.pending.length; i++) {
    if (view.pending[i].norm === n) return settleAt(i);
  }
  var bare = normText((text || '').replace(/<[^>]*>/g, ' '));
  for (i = 0; i < view.pending.length; i++) {
    if (view.pending[i].norm && bare.indexOf(view.pending[i].norm) >= 0) return settleAt(i);
  }
}

function settleAt(i) {
  view.pending[i].node.remove();
  view.pending.splice(i, 1);
}

function dropPending(p) {
  var i = view.pending.indexOf(p);
  if (i >= 0) view.pending.splice(i, 1);
}

function normText(s) { return (s || '').replace(/\s+/g, ' ').trim(); }

/* ---------------- drafts ---------------- */

/* Per-chat drafts (grove-316). Keyed by chat address, restored only for
 * THAT address, so opening chat B can never show chat A's text — the
 * wrong-agent rule (grove-116) holds by construction. Storage is a
 * nicety: a private window may have none, so every touch is guarded and
 * a failure just means no draft. */
var DRAFT_PREFIX = 'gv-chat-draft:';
var DRAFT_TTL_MS = 7 * 24 * 3600 * 1000;
var draftTimer = null;

function loadDraft(a) {
  try {
    var d = JSON.parse(localStorage.getItem(DRAFT_PREFIX + a) || 'null');
    return d && typeof d.text === 'string' ? d.text : '';
  } catch (_) { return ''; }
}

function saveDraft(a, v) {
  try {
    if (v.trim()) localStorage.setItem(DRAFT_PREFIX + a, JSON.stringify({ text: v, at: Date.now() }));
    else localStorage.removeItem(DRAFT_PREFIX + a);
  } catch (_) { /* no storage — no drafts */ }
}

function pruneDrafts() {
  try {
    var stale = [];
    for (var i = 0; i < localStorage.length; i++) {
      var k = localStorage.key(i);
      if (k.indexOf(DRAFT_PREFIX) !== 0) continue;
      var d = null;
      try { d = JSON.parse(localStorage.getItem(k)); } catch (_) { /* unreadable: drop */ }
      if (!d || !(Date.now() - d.at < DRAFT_TTL_MS)) stale.push(k);
    }
    stale.forEach(function (k) { localStorage.removeItem(k); });
  } catch (_) { /* no storage — nothing to prune */ }
}

function isTouch() {
  return window.matchMedia && window.matchMedia('(pointer: coarse)').matches;
}

function autosize() {
  var t = el('text');
  t.style.height = 'auto';
  t.style.height = Math.min(t.scrollHeight, window.innerHeight * 0.4) + 'px';
}

/* renderKeys grows the raw-key row when the pane scrape sees a modal.
 * Detection is garnish by house rule — it reads the pane, and a pane is
 * the one thing here that is not the transcript — so a miss must be
 * survivable: the row says what to do when it is wrong.
 *
 * grove-308: a menu's options arrive with their labels, so the row shows
 * "Banana", not a bare 2. A digit answers a single-select outright and
 * toggles a multi-select row (☐/☑); `next ⇥` (Tab) walks AskUserQuestion's
 * pages to its Submit page, itself a numbered menu. The free-text row
 * ("Type something.") moves the menu's caret into a text input — once the
 * scrape sees it there (typing), the composer's ordinary send answers it. */
var keyNames = { esc: 'esc', tab: 'next ⇥', up: '↑', down: '↓', enter: 'enter ⏎' };

/* wasTyping: focus only on the edge into typing (grove-318) — re-focusing
 * on every picker event re-pops the Android keyboard after each toggle. */
var wasTyping = false;

function renderKeys(p) {
  var box = el('keys');
  box.textContent = '';
  syncSend();
  if (!p || !p.detected) {
    wasTyping = false;
    if (blocked()) { renderBlocked(box); return; }
    document.body.classList.remove('picker');
    return;
  }
  document.body.classList.add('picker');
  var label = p.prompt || 'the chat is asking something — answer with a key';
  if (p.typing) label = 'type your answer below and send — ' + label;
  box.append(h('div', 'label', label));
  /* AskUserQuestion's Submit page (grove-334): the picks it is about to
   * submit, from the pane's own review text, above `Submit answers`. */
  (p.review || []).forEach(function (r) { box.append(h('div', 'review', r)); });
  var labelled = {};
  (p.options || []).forEach(function (o) {
    labelled[o.key] = true;
    var text = o.label;
    /* "Chat about this" leaves the question rather than picking an answer,
     * so it gets no checkbox even on a multi-select. */
    if (p.kind === 'multi' && !o.free && !/^chat about this/i.test(o.label)) text = (o.checked ? '☑ ' : '☐ ') + text;
    else if (o.free) text = '✎ ' + text;
    /* grove-333: an unnumbered "select" menu (the folder-trust dialog)
     * has no digit to press — the option is sent by number and the
     * server walks the caret to it against a fresh capture. */
    var b = p.kind === 'select'
      ? keyButton(box, { option: Number(o.key) }, (p.caret === o.key ? '❯ ' : '') + text)
      : keyButton(box, o.key, o.key + ' · ' + text);
    b.classList.add('opt');
    if (o.checked) b.classList.add('on');
  });
  (p.keys || []).forEach(function (k) {
    /* A select's option buttons already carry the whole answer — and
     * no esc: on the folder-trust dialog "Esc to cancel" exits claude
     * exactly like "No, exit" does (verified, grove-333). */
    if (p.kind === 'select') return;
    if (!labelled[k]) keyButton(box, k, keyNames[k] || k);
  });
  if (p.typing && !wasTyping) el('text').focus();
  wasTyping = !!p.typing;
}

/* blocked (grove-334): the pane read says a modal holds the turn, but the
 * picker scrape found nothing it can offer keys for. Before this the page
 * showed nothing at all and the composer invited a message the server
 * would refuse — a blocked chat that looked ready. */
function blocked() {
  var t = view.turn && Date.now() >= view.turnHold ? view.turn : null;
  return !!(view.es && t && t.state === 'waiting' && !(view.picker && view.picker.detected));
}

/* renderBlocked is the universal escape hatch for a prompt the phone does
 * not understand: say so, and offer the pane itself to read. */
function renderBlocked(box) {
  document.body.classList.add('picker');
  box.append(h('div', 'label', 'the chat is showing a prompt the phone can’t read'));
  var b = h('button', '', 'show pane');
  b.onclick = function () { openPaneSheet(view.addr); };
  box.append(b);
}

/* syncSend gates the send button on blocked(): the server refuses a send
 * into a modal anyway (409), and a live-looking button is the lie. Only a
 * writable chat's button is touched — read-only rows stay disabled. */
function syncSend() {
  var c = chatByAddr(view.addr);
  if (!c || !c.writable) return;
  el('send').disabled = blocked();
}

/* openPaneSheet is "show pane": the bottom of the chat's pane as the
 * server captured it, monospace, read-only, with a refresh. */
function openPaneSheet(a) {
  var panel = el('sheet-panel');
  panel.textContent = '';
  panel.append(h('div', 'sheet-title', 'the pane, as it is now — read-only'));
  var pre = h('pre', 'pane', 'reading…');
  panel.append(pre);
  var load = function () {
    api('/api/chats/' + encodeURIComponent(a) + '/pane').then(function (j) {
      pre.textContent = j.pane || '(empty)';
      pre.scrollTop = pre.scrollHeight;
    }, function (e) { pre.textContent = String(e && e.message ? e.message : e); });
  };
  var again = h('button', 'row', '');
  again.append(h('div', 'title', 'refresh'));
  again.onclick = load;
  panel.append(again);
  var cancel = h('button', 'cancel', 'close');
  cancel.onclick = closeSheet;
  panel.append(cancel);
  var sheet = el('sheet');
  sheet.onclick = function (ev) { if (ev.target === sheet) closeSheet(); };
  sheet.hidden = false;
  load();
}

function keyButton(box, k, text) {
  var b = h('button', '', text);
  b.onclick = function () {
    box.classList.add('busy');
    api('/api/chats/' + encodeURIComponent(view.addr) + '/keys', typeof k === 'string' ? { key: k } : k)
      .catch(showError)
      .then(function () { box.classList.remove('busy'); });
  };
  box.append(b);
  return b;
}

/* ---------------- notifications (grove-305) ---------------- */

/* The phone is in a pocket, and the page is the only thing that knows the
 * agent is done or asking. Page-side only — no push service: a system
 * notification fires while the page is open or backgrounded, and nothing
 * once the browser has frozen it. Off by default, opt-in per device, and
 * every API is feature-detected, so a browser without them is today's page.
 *
 * `live` is the replay guard. A chat opens by replaying its whole history
 * down the same stream that then follows it, with no marker between the
 * two, so replay is "the stream until it has been quiet for SETTLE_MS" —
 * hundreds of replayed turn ends land in well under that. `waiting` is the
 * set of chats the page has seen sitting on a picker; `picked` is the last
 * picker state per chat, so only the edge into one alerts. */
var NOTIFY_KEY = 'gv-chat:notify';
var SETTLE_MS = 1500;
var alerts = { live: false, settle: null, waiting: {}, picked: {}, endSeq: 0, rows: null };

function notifySupported() {
  return 'Notification' in window && window.isSecureContext;
}

function notifyOn() {
  if (!notifySupported() || Notification.permission !== 'granted') return false;
  try { return localStorage.getItem(NOTIFY_KEY) === '1'; } catch (_) { return false; }
}

function paintNotify() {
  var b = el('notify');
  b.hidden = !notifySupported();
  var on = notifyOn();
  b.textContent = on ? '🔔' : '🔕';
  b.setAttribute('aria-pressed', on ? 'true' : 'false');
  b.setAttribute('aria-label', on ? 'notifications on' : 'notifications off');
}

function toggleNotify() {
  var set = function (v) {
    try { localStorage.setItem(NOTIFY_KEY, v ? '1' : '0'); } catch (_) { /* per-device nicety */ }
    if (!v) { alerts.waiting = {}; syncBadge(true); }
    paintNotify();
    syncPolling();
  };
  if (notifyOn()) { set(false); return; }
  if (Notification.permission === 'granted') { set(true); return; }
  /* Called from the tap itself: Chrome only prompts on a user gesture. A
   * refusal leaves the bell off, and the browser remembers it. */
  Promise.resolve(Notification.requestPermission()).then(function (perm) {
    set(perm === 'granted');
  }, function () { paintNotify(); });
}

/* Each entry pushes the settle point back; the stream's open starts it, so
 * an empty transcript still goes live, and a slow tailnet connect cannot
 * go live before the replay has even begun. It covers the first turn and
 * picker reads too (StreamPoll, 1s after connect): those are the chat's
 * state on arrival, not news. A cache restore's `?since=` catch-up is the
 * same — what landed while the chat was off screen is read on screen, not
 * notified. Once live, a reconnect's resumed entries are real news and
 * stay live. */
function settleReplay() {
  if (alerts.live) return;
  clearTimeout(alerts.settle);
  alerts.settle = setTimeout(function () { alerts.live = true; }, SETTLE_MS);
}

/* onTurnEnd is the one place a finished turn alerts from; renderWorking
 * calls it on the shown working → idle edge. `endSeq` caps it at one per
 * turn: a real turn always lands transcript entries, while a flicker in
 * the shown state (a pane read catching up after the send hold) lands
 * none, so an end at a seq already alerted on is the same end. */
function onTurnEnd() {
  if (!alerts.live || !view.addr || view.maxSeq <= alerts.endSeq) return;
  alerts.endSeq = view.maxSeq;
  notify(view.addr, 'finished its turn');
}

function notePicker(a, p) {
  var on = !!(p && p.detected);
  var edge = on && !alerts.picked[a];
  alerts.picked[a] = on;
  if (on) alerts.waiting[a] = true; else delete alerts.waiting[a];
  syncBadge();
  if (!edge || !alerts.live || !notifyOn()) return;
  /* Vibrate on the picker only: a turn ending is not worth a buzz, a
   * question blocking the agent is. */
  if (navigator.vibrate) navigator.vibrate([80, 60, 80]);
  notify(a, (p.prompt || 'is asking something').slice(0, 160));
}

/* A notification only fires while the page is not being looked at — a
 * visible page already shows both events. Tagged by address, so a chat's
 * alerts replace each other instead of stacking. Android Chrome refuses
 * `new Notification()` outright; the service worker's showNotification is
 * the path there, and sw.js routes the tap back to the chat. */
function notify(a, body) {
  if (!notifyOn() || document.visibilityState === 'visible') return;
  var c = chatByAddr(a);
  var title = c ? chatTitle(c) : a;
  var hash = '#/c/' + encodeURIComponent(a);
  var opts = { body: body, tag: 'gv-chat:' + a, renotify: true, icon: 'icon-192.png', data: { hash: hash } };
  var direct = function () {
    try {
      var n = new Notification(title, opts);
      n.onclick = function () { window.focus(); location.hash = hash; n.close(); };
    } catch (_) { /* no constructor here (Android): nothing more to try */ }
  };
  if (!('serviceWorker' in navigator)) { direct(); return; }
  navigator.serviceWorker.getRegistration().then(function (reg) {
    return reg ? reg.showNotification(title, opts) : direct();
  }).catch(direct);
}

/* noteRows is "needs you" off the LIST (grove-302): every live chat's
 * `waiting`, which the server reads from a pane capture per row. Before it,
 * only the open chat's stream knew a picker was up, so a question blocking
 * any other chat stayed silent. `rows` is the last list's waiting set; null
 * until the first load, which is a baseline and never alerts — opening the
 * app is not news. Only the false→true edge alerts, and never for the open
 * chat: its own stream's picker event (notePicker) already does that. */
function noteRows() {
  syncChatHeader();
  var now = {};
  view.chats.forEach(function (c) { if (c.waiting) now[addr(c)] = true; });
  var base = alerts.rows;
  alerts.rows = now;
  syncBadge();
  if (!base || !notifyOn()) return;
  Object.keys(now).forEach(function (a) {
    if (base[a] || (view.es && a === view.addr)) return;
    notify(a, 'needs you — it is waiting on a question');
  });
}

/* wantLabel re-reads the list once when the open chat is still untitled
 * and something was just said in it (grove-334): the list is not polled
 * while a chat is open, so a fresh chat's label would otherwise only show
 * up after leaving it. One fetch per LABEL_MS at most; replay of a chat
 * that already has a label costs nothing. */
var LABEL_MS = 3000;
var labelTimer = null;
function wantLabel() {
  var c = chatByAddr(view.addr);
  if (!c || c.label || labelTimer) return;
  labelTimer = setTimeout(function () {
    labelTimer = null;
    loadChats().catch(function () { /* garnish: the session name stays */ });
  }, LABEL_MS);
}

/* syncChatHeader retitles the open chat when its row changes under it
 * (grove-334): a fresh chat has no label until its first prompt lands, so
 * the header showed the session name for as long as the chat was open. */
function syncChatHeader() {
  if (!view.es || !view.addr) return;
  var c = chatByAddr(view.addr);
  if (c && el('title').textContent !== chatTitle(c)) el('title').textContent = chatTitle(c);
}

/* The app badge counts chats sitting on a picker: the list's `waiting`
 * rows plus the open chat's own stream, which sees one within a second.
 * It only exists on an installed PWA (grove-296), and only while
 * notifications are on — off is exactly the old page. */
function syncBadge(force) {
  if (!navigator.setAppBadge || (!force && !notifyOn())) return;
  var set = {};
  Object.keys(alerts.waiting).forEach(function (a) { set[a] = true; });
  if (alerts.rows) Object.keys(alerts.rows).forEach(function (a) { set[a] = true; });
  var n = Object.keys(set).length;
  (n && notifyOn() ? navigator.setAppBadge(n) : navigator.clearAppBadge()).catch(function () { /* garnish */ });
}

/* ---------------- routing ---------------- */

function render() {
  keepChat();
  closeStream();
  closeSheet();
  var parts = (location.hash || '#/').slice(1).split('/');
  if (parts[1] === 'w' && parts[2]) screenWorkspace(decodeURIComponent(parts[2]));
  else if (parts[1] === 'c' && parts[2]) screenChat(decodeURIComponent(parts[2]));
  else screenHome();
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
  var open = Object.keys(view.hist).filter(function (k) { return view.hist[k]; }).sort().join(',');
  var parts = [location.hash || '#/', view.profiles.length, view.loaded ? 1 : 0, open, view.version, view.workspaces.join(',')];
  view.chats.forEach(function (c) {
    parts.push(c.workspace, c.kind, c.session || '', c.session_id || '',
      chatTitle(c), c.busy ? 1 : 0, c.writable ? 1 : 0, c.waiting ? 1 : 0, c.turn || '', ago(activeAt(c)));
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
  if (poll.inflight) return;
  if (isListScreen() && !document.hidden) {
    if (!el('sheet').hidden) return;
  } else if (!notifyOn() || Date.now() - poll.at < WATCH_MS) {
    /* Off screen, the list is only read to watch for `waiting` — and
     * only for an operator who asked to be notified. repaintList below is
     * inert there: listSig is null off a list screen. */
    return;
  }
  poll.inflight = true;
  poll.at = Date.now();
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

/* The list is wanted on a visible list screen, and — with notifications
 * on — everywhere, since watching for `waiting` is the point (grove-302).
 * Otherwise nothing runs: a backgrounded tab or an open chat costs
 * literally nothing rather than a guarded wake-up. */
function wantList() {
  return (isListScreen() && !document.hidden) || notifyOn();
}

function syncPolling() {
  syncFeed(wantList());
  syncTimer();
}

/* The fetch interval runs only while the list is wanted AND the stream is
 * not carrying it — the 5s/15s beats are the fallback, not the transport. */
function syncTimer() {
  var want = wantList() && !feed.healthy;
  if (want && !poll.timer) poll.timer = setInterval(refreshList, POLL_MS);
  else if (!want && poll.timer) { clearInterval(poll.timer); poll.timer = null; }
  var ages = feed.healthy && isListScreen() && !document.hidden;
  if (ages && !feed.ages) feed.ages = setInterval(repaintIdle, AGES_MS);
  else if (!ages && feed.ages) { clearInterval(feed.ages); feed.ages = null; }
}

/* repaintList, minus the one moment it must not run: under an open sheet,
 * which render() would dismiss mid-answer. A skipped repaint is caught by
 * the next beat. */
function repaintIdle() {
  if (el('sheet').hidden) repaintList();
}

/* syncFeed opens the list stream (grove-307) when the list is wanted and
 * closes it when not. ONE server-side enumeration feeds every phone, and a
 * `chats` event arrives only when the list changed — the same envelope as
 * GET /api/chats. On an error the browser reconnects on its own; until it
 * lands, the poll takes over. A stream the browser gave up on (an older
 * server 404s the route) is dropped, and the next navigation or refocus
 * tries again while the poll carries the list. */
function syncFeed(want) {
  if (want && !feed.es && window.EventSource) openFeed();
  else if (!want && feed.es) {
    feed.es.close();
    feed.es = null;
    feed.healthy = false;
  }
}

function openFeed() {
  var es = feed.es = new EventSource('/api/chats/events');
  /* First on every connect — so a reconnect to a restarted server says so. */
  es.addEventListener('version', function (ev) {
    try { noteVersion(JSON.parse(ev.data)); } catch (_) { /* garnish */ }
  });
  es.addEventListener('chats', function (ev) {
    var j;
    try { j = JSON.parse(ev.data); } catch (_) { return; }
    view.chats = j.chats || [];
    view.loaded = true;
    noteRows();
    repaintIdle();
  });
  es.onopen = function () {
    feed.healthy = true;
    document.body.classList.remove('offline');
    syncTimer();
  };
  es.onerror = function () {
    feed.healthy = false;
    if (es.readyState === EventSource.CLOSED && feed.es === es) feed.es = null;
    syncTimer();
  };
}

function refresh() {
  /* Profiles ride along with the chat list and never block it: the picker
   * is garnish, the chats are the app. */
  loadVersion();
  return Promise.all([loadProfiles(), loadWorkspaces()]).then(loadChats).then(render, function (e) { render(); showError(e); });
}

window.addEventListener('hashchange', function () {
  render();
  syncPolling();
  /* Coming back from a chat must not show the list as stale as when it was
   * left — the cached rows paint instantly, then this catches them up. A
   * healthy stream has kept them current already. */
  if (!feed.healthy) refreshList();
});
/* Unlocking the phone or switching back to the tab is the other moment the
 * list is guaranteed stale, and the one the operator notices most. */
document.addEventListener('visibilitychange', function () {
  syncPolling();
  refreshList();
  /* A resumed phone also asks which server it is talking to (grove-286). */
  if (!document.hidden) loadVersion();
});
/* Regaining the network refreshes the LISTS. A chat screen is deliberately
 * left alone: re-rendering it would tear down a stream that is already
 * resuming on its own (grove-259). Its EventSource reconnects on its own, carrying
 * Last-Event-ID, and onopen clears the offline class when it lands. */
window.addEventListener('online', function () { if (isListScreen()) refresh(); });
window.addEventListener('offline', function () { document.body.classList.add('offline'); });
el('refresh').onclick = refresh;
el('notify').onclick = toggleNotify;
/* A tap dismisses outright; holding it down pauses the auto-dismiss timer
 * so the toast does not vanish out from under a reader mid-press, and
 * letting go without that turning into a click (e.g. a scroll) resumes
 * the countdown rather than leaving it paused forever. */
el('toast').onclick = hideToast;
el('toast').addEventListener('pointerdown', function () { clearTimeout(toastTimer); });
el('toast').addEventListener('pointerup', restartToastTimer);

/* `stop` interrupts a running turn with a single Esc — the same key and the
 * same /keys route the picker strip already uses (grove-225), just offered
 * while the turn is running rather than only when the pane scrape sees a
 * modal (grove-299). Exactly one Esc per tap: a second one opens Claude
 * Code's rewind picker, which a phone would then need to dismiss, so `.busy`
 * (shared styling with #keys) blocks re-taps for a beat after the request
 * settles rather than for the request's own duration alone. */
var stopBusy = false;
el('stop').onclick = function () {
  if (stopBusy || !view.addr) return;
  stopBusy = true;
  el('stop').classList.add('busy');
  api('/api/chats/' + encodeURIComponent(view.addr) + '/keys', { key: 'esc' })
    /* Optimistic: the stream corrects this back to "working…" if the turn
     * is in fact still going (an Esc during a tool call, say). */
    .then(function () { view.stopped = true; holdTurn(); setWorking(false); })
    .catch(showError)
    .then(function () {
      setTimeout(function () {
        stopBusy = false;
        el('stop').classList.remove('busy');
      }, 1200);
    });
};

/* Tapping the pill jumps to the bottom and hides it; scrolling back within
 * the stick threshold on your own does the same without a tap — either way
 * the pill only ever means "you are not at the bottom right now" (grove-304).
 * `auto` (instant) over `smooth` under reduced motion, per the composer's
 * own pulse animation a few lines up in index.html. */
el('jump').onclick = function () {
  var main = el('main');
  var reduce = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  main.scrollTo({ top: main.scrollHeight, behavior: reduce ? 'auto' : 'smooth' });
  el('jump').hidden = true;
};
el('main').addEventListener('scroll', function () {
  var main = el('main');
  if (main.scrollHeight - main.scrollTop - main.clientHeight < 120) el('jump').hidden = true;
});

if (window.marked) window.marked.use({ gfm: true, breaks: true });
pruneDrafts();
paintNotify();
render();
refresh();
syncPolling();

/* The service worker caches the shell only (never /api), so a phone that
 * has lost the tailnet opens to "not connected" instead of a blank page.
 * Secure-context only, which is why the deploy needs tailnet HTTPS. */
if ('serviceWorker' in navigator && window.isSecureContext) {
  navigator.serviceWorker.register('sw.js').catch(function () { /* offline shell is a nicety, never a requirement */ });
  /* A tapped notification (grove-305): sw.js focuses this window and says
   * which chat; only a chat route is honoured. */
  navigator.serviceWorker.addEventListener('message', function (ev) {
    var hash = ev.data && ev.data.gvChatNav;
    if (typeof hash === 'string' && hash.indexOf('#/c/') === 0) location.hash = hash;
  });
}
