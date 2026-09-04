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
var view = { chats: [], profiles: [], loaded: false, es: null, maxSeq: 0, addr: null };
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
 * append come down ONE stream, so there is no seam to get wrong: `since`
 * makes an EventSource reconnect (a phone waking up, a tailnet blip)
 * resume where it left off, and the seq check makes a duplicate replay
 * harmless either way. */
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
 * would print. Assistant prose goes through marked; a tool call collapses
 * to one line the reader can open. Thinking is collapsed by default: on a
 * phone it is the difference between a readable conversation and a wall. */
function appendEntry(e) {
  var main = el('main');
  var stick = main.scrollHeight - main.scrollTop - main.clientHeight < 120;
  var node;
  if (e.kind === 'tool_use' || e.kind === 'tool_result') {
    node = h('details', 'tool');
    var mark = e.kind === 'tool_use' ? '▸ ' : '◂ ';
    node.append(h('summary', '', mark + (e.tool || 'tool') + ' — ' + oneLine(e.text)));
    node.append(h('pre', '', e.text || ''));
  } else if (e.kind === 'thinking') {
    node = h('details', 'tool think');
    node.append(h('summary', '', '… thinking'));
    node.append(h('pre', '', e.text || ''));
  } else if (e.role === 'assistant') {
    node = h('div', 'msg assistant');
    /* Safe because of the server's Content-Security-Policy, not because
     * marked sanitizes (it does not, since v8). See index.html's note. */
    node.innerHTML = window.marked ? window.marked.parse(e.text || '') : '';
    if (!window.marked) node.textContent = e.text || '';
  } else {
    node = h('div', 'msg user', e.text || '');
  }
  main.append(node);
  if (stick) main.scrollTop = main.scrollHeight;
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
      .then(function () { text.value = ''; autosize(); })
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
window.addEventListener('online', refresh);
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
