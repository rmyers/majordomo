const chatView = document.getElementById('chat-view');
const indexView = document.getElementById('index-view');

if (chatView) {
  initializeChatView();
} else if (indexView) {
  initializeIndexView();
}

function initializeIndexView() {
  // No form, no input, no fetch — "New Chat" is a plain
  // <a href="/chat/new"> in the template, which the server handles by
  // creating a session and redirecting. All that's left for JS here is
  // showing which sessions are currently active.

  // Activity dots: each session card is expected to carry
  // data-session-id="<id>" and contain a child with class
  // "session-activity-dot" — add both to your grid template if they're
  // not there yet.
  function setActiveDot(sessionId, active) {
    const item = document.querySelector(`[data-session-id="${sessionId}"]`);
    if (!item) return; // session not in the currently rendered grid
    const dot = item.querySelector('.session-activity-dot');
    if (dot) dot.classList.toggle('active', active);
  }

  // Seed initial state — a session can already be mid-turn if the user
  // navigated away from its chat view and back to the index, since
  // StreamManager keeps running server-side regardless of what page is
  // showing.
  window.go.main.App.ActiveSessions().then((ids) => {
    (ids || []).forEach((id) => setActiveDot(id, true));
  });

  // Then stay current via the global broadcast channel. One listener
  // covers every session on the grid — no per-session subscriptions.
  window.runtime.EventsOn('sessions:activity', (evt) => {
    const { sessionId, active } = evt || {};
    setActiveDot(sessionId, active);
  });
}

function initializeChatView() {
  const messagesEl = document.getElementById('messages');
  const inputEl = document.getElementById('input');
  const sendBtn = document.getElementById('send-btn');
  const cancelBtn = document.getElementById('cancel-btn'); // add this button to your template
  const statusDot = document.getElementById('status-dot');
  const statusText = document.getElementById('status-text');
  const sessionId = window.currentSessionId;

  inputEl.addEventListener('input', () => {
    inputEl.style.height = 'auto';
    inputEl.style.height = Math.min(inputEl.scrollHeight, 120) + 'px';
  });
  inputEl.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  });
  sendBtn.addEventListener('click', sendMessage);
  if (cancelBtn) {
    cancelBtn.addEventListener('click', () => {
      window.go.main.App.CancelStream(sessionId, false);
    });
  }

  function setStatus(state, text) {
    statusDot.className = state;
    statusText.textContent = text;
  }

  // Every chat-window update — the new user bubble, the "thinking"
  // placeholder, streamed tokens, tool notices, the final response,
  // errors, cancellation — arrives here as ready-to-insert HTML. This
  // listener never builds a DOM node itself; it only inserts or swaps
  // whatever the server already rendered.
  window.runtime.EventsOn(`stream:${sessionId}`, (evt) => {
    const { event, target, html } = evt || {};

    if (html) {
      const el = target ? document.getElementById(target) : null;
      if (el) {
        el.outerHTML = html;
      } else {
        messagesEl.insertAdjacentHTML('beforeend', html);
      }
      messagesEl.scrollTop = messagesEl.scrollHeight;
    }

    switch (event) {
      case 'status':
        setStatus('connected', 'thinking...');
        break;
      case 'queued':
        setStatus('connected', 'queued...');
        break;
      case 'error':
        setStatus('error', 'error');
        sendBtn.disabled = false;
        break;
      case 'cancelled':
        setStatus('', 'cancelled');
        sendBtn.disabled = false;
        break;
      case 'done':
        setStatus('', 'ready');
        sendBtn.disabled = false;
        break;
    }
  });

  function sendMessage() {
    const query = inputEl.value.trim();
    if (!query) return;
    inputEl.value = '';
    inputEl.style.height = 'auto';
    sendBtn.disabled = true;
    setStatus('connected', 'sending...');

    window.go.main.App.SendMessage(query, sessionId).catch((err) => {
      setStatus('error', 'error');
      sendBtn.disabled = false;
    });
  }
}
