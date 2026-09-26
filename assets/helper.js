(function() {
  // Browser side of the visual companion.
  //
  // Upstream used WebSocket for both directions. This build uses SSE for the
  // single server->browser message there is ("reload") and POST for
  // browser->server clicks. That trades a hand-rolled RFC 6455 codec for
  // net/http on the server and a native EventSource here, which reconnects on
  // its own.
  //
  // The contract with authored screens is unchanged: elements carrying
  // [data-choice] report clicks, and `toggleSelect` plus the `brainstorm` API
  // work exactly as before.

  const STALE_AFTER_MS = 45000;   // no heartbeat for this long => treat as down
  const CHECK_INTERVAL_MS = 5000;
  const OFFLINE_AFTER_MS = 15000; // show the paused overlay after this long down

  // Pure: exported for unit tests.
  function isStale(lastMessageAt, now, threshold) {
    if (!lastMessageAt) return false;
    return now - lastMessageAt > threshold;
  }
  function shouldShowOverlay(downSince, now, threshold) {
    if (!downSince) return false;
    return now - downSince >= threshold;
  }
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = { isStale, shouldShowOverlay, STALE_AFTER_MS, CHECK_INTERVAL_MS, OFFLINE_AFTER_MS };
  }

  // Everything below is browser-only; bail out when loaded in Node (tests).
  if (typeof window === 'undefined') return;

  let es = null;
  let lastMessageAt = 0;
  let downSince = null;
  let everConnected = false;
  let overlayShown = false;

  function sessionKey() {
    try {
      return window.sessionStorage && window.sessionStorage.getItem('brainstorm-session-key');
    } catch (e) {}
    return null;
  }

  // Reflect connection state in the frame's status pill (absent on full-doc screens).
  function setStatus(state) {
    const el = document.querySelector('.status');
    if (!el) return;
    const map = {
      connecting:   ['Connecting…',   'var(--text-tertiary)'],
      connected:    ['Connected',     'var(--success)'],
      reconnecting: ['Reconnecting…', 'var(--warning)'],
      disconnected: ['Disconnected',  'var(--error)']
    };
    const [text, color] = map[state] || map.disconnected;
    el.textContent = text;
    el.style.setProperty('--status-color', color);
  }

  // Self-styled so it works on framed and full-document screens alike.
  function showOverlay() {
    if (overlayShown) return;
    overlayShown = true;
    const el = document.createElement('div');
    el.id = 'vc-paused';
    el.style.cssText = 'position:fixed;inset:0;z-index:99999;display:flex;' +
      'align-items:center;justify-content:center;padding:2rem;text-align:center;' +
      'background:rgba(20,20,22,0.92);color:#f5f5f7;font-family:system-ui,sans-serif';
    el.innerHTML = '<div style="max-width:480px">' +
      '<h2 style="margin:0 0 .5rem;font-weight:600">Companion paused</h2>' +
      '<p style="margin:0;opacity:.85">This brainstorm companion has stopped. ' +
      'Ask your coding agent to bring it back — this page reconnects automatically.</p></div>';
    if (document.body) document.body.appendChild(el);
  }

  function clearOverlay() {
    overlayShown = false;
    const el = document.getElementById('vc-paused');
    if (el && el.parentNode) el.parentNode.removeChild(el);
  }

  function markDown() {
    if (downSince === null) downSince = Date.now();
    setStatus(everConnected ? 'disconnected' : 'connecting');
    if (shouldShowOverlay(downSince, Date.now(), OFFLINE_AFTER_MS)) showOverlay();
  }

  function connect() {
    if (es) { try { es.close(); } catch (e) {} es = null; }
    setStatus(everConnected ? 'reconnecting' : 'connecting');

    const key = sessionKey();
    es = new EventSource('/events' + (key ? '?key=' + encodeURIComponent(key) : ''));

    es.addEventListener('hello', onMessage);
    es.addEventListener('message', onMessage);

    // EventSource retries by itself, so onerror only updates the UI; the
    // watchdog decides when the stream is truly dead.
    es.onerror = function() { markDown(); };
  }

  function onMessage(e) {
    lastMessageAt = Date.now();
    downSince = null;
    clearOverlay();
    setStatus('connected');
    everConnected = true;

    let data = null;
    try { data = JSON.parse(e.data); } catch (err) {}

    if (data && data.type === 'reload') {
      window.location.reload();
    }
  }

  // The server sends a heartbeat every 15s. If nothing arrives for a while the
  // stream is dead even though EventSource still believes it is open — rebuild
  // it rather than waiting on the browser's own backoff.
  function watchdog() {
    const now = Date.now();
    if (lastMessageAt && isStale(lastMessageAt, now, STALE_AFTER_MS)) {
      lastMessageAt = now; // avoid re-triggering on every tick
      markDown();
      connect();
      return;
    }
    if (downSince !== null && shouldShowOverlay(downSince, now, OFFLINE_AFTER_MS)) {
      showOverlay();
    }
  }

  function sendEvent(event) {
    event.timestamp = Date.now();
    const key = sessionKey();
    const url = '/events' + (key ? '?key=' + encodeURIComponent(key) : '');
    try {
      fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(event),
        keepalive: true
      }).catch(function() {});
    } catch (err) {}
  }

  // Capture clicks on choice elements
  document.addEventListener('click', function(e) {
    const target = e.target.closest('[data-choice]');
    if (!target) return;

    // In a multi-select container a click can mean "add" or "remove", and a
    // bare click event cannot tell those apart — "selected A and C, then
    // deselected C" would arrive looking exactly like "selected C twice".
    // The authoritative set goes with every event so the agent never has to
    // reconstruct it.
    const container = target.closest('.options[data-multiselect], .cards[data-multiselect]');
    let selected = null;
    if (container) {
      selected = Array.from(container.querySelectorAll('.selected'))
        .map(function(o) { return o.dataset.choice; });
      const box = document.querySelector('.selected-text');
      if (box) box.textContent = selected.length ? 'Selected: ' + selected.join(', ') : '';
    }

    sendEvent({
      type: 'click',
      text: target.textContent.trim(),
      choice: target.dataset.choice,
      id: target.id || null,
      selected: selected
    });
  });

  // Frame UI: selection tracking
  window.selectedChoice = null;

  // Confirm the selection in words as well as colour: a highlight alone is
  // easy to miss, and it conveys nothing to anyone who cannot see it.
  function reflectSelection(el) {
    const box = document.querySelector('.selected-text');
    if (!box) return;
    if (!el) { box.textContent = ''; return; }
    const heading = el.querySelector('h3, .card-body h3, .letter');
    const label = (heading && heading.textContent.trim()) || el.dataset.choice || '';
    box.textContent = label ? 'Selected: ' + label : '';
  }

  window.toggleSelect = function(el) {
    const container = el.closest('.options') || el.closest('.cards');
    const multi = container && container.dataset.multiselect !== undefined;
    if (container && !multi) {
      container.querySelectorAll('.option, .card').forEach(function(o) { o.classList.remove('selected'); });
    }
    if (multi) {
      el.classList.toggle('selected');
    } else {
      el.classList.add('selected');
    }
    window.selectedChoice = el.dataset.choice;
    reflectSelection(el.classList.contains('selected') ? el : null);
  };

  // Expose API for explicit use
  window.brainstorm = {
    send: sendEvent,
    choice: function(value, metadata) {
      sendEvent(Object.assign({ type: 'choice', value: value }, metadata || {}));
    }
  };

  connect();
  setInterval(watchdog, CHECK_INTERVAL_MS);
  // Returning to a backgrounded tab is the most common way to find a dead
  // stream; check immediately instead of waiting for the next tick.
  document.addEventListener('visibilitychange', function() {
    if (!document.hidden) watchdog();
  });
})();
