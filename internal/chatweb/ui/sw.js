/* gv chat service worker (grove-218).
 *
 * It caches the SHELL and nothing else. Its whole job is that a phone whose
 * tailnet has dropped — the common case: the operator walked out of range,
 * or the key expired — opens to the app saying "not connected" instead of
 * the browser's blank failure page.
 *
 * Never cache /api: a chat list or a transcript served from cache is a lie
 * about a live agent, and the one thing this app must not do is show a
 * stale conversation as if it were current. The shell is network-FIRST too,
 * so a `gv update` that ships a new page is picked up on the next load
 * rather than pinned forever by a service worker nobody remembers exists.
 */
'use strict';

var CACHE = 'gv-chat-shell-v1';
var SHELL = ['./', 'index.html', 'app.js', 'marked.min.js'];

self.addEventListener('install', function (e) {
  e.waitUntil(caches.open(CACHE).then(function (c) { return c.addAll(SHELL); }).then(function () {
    return self.skipWaiting();
  }));
});

self.addEventListener('activate', function (e) {
  e.waitUntil(caches.keys().then(function (keys) {
    return Promise.all(keys.map(function (k) { return k === CACHE ? null : caches.delete(k); }));
  }).then(function () { return self.clients.claim(); }));
});

self.addEventListener('fetch', function (e) {
  var url = new URL(e.request.url);
  if (e.request.method !== 'GET') return;
  if (url.origin !== self.location.origin) return;
  if (url.pathname.indexOf('/api/') === 0) return; // live data is never cached
  e.respondWith(
    fetch(e.request).then(function (res) {
      if (res && res.ok) {
        var copy = res.clone();
        caches.open(CACHE).then(function (c) { c.put(e.request, copy); });
      }
      return res;
    }).catch(function () {
      return caches.match(e.request).then(function (hit) {
        return hit || caches.match('index.html');
      });
    })
  );
});
