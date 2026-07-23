const chatView = document.getElementById('chat-view');
const indexView = document.getElementById('index-view');

if (chatView) {
  initializeChatView();
} else if (indexView) {
  initializeIndexView();
}

function initializeIndexView() {
  const inputEl = document.getElementById('input');
  const sendBtn = document.getElementById('send-btn');
  inputEl.addEventListener('input', () => {
    inputEl.style.height = 'auto';
    inputEl.style.height = Math.min(inputEl.scrollHeight, 120) + 'px';
  });
  inputEl.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      startNewSession();
    }
  });
  sendBtn.addEventListener('click', startNewSession);

  function startNewSession() {
    const query = inputEl.value.trim();
    if (!query) return;
    fetch('/api/sessions', { method: 'POST' })
      .then(res => res.json())
      .then(data => {
        window.location.href = `/chat/${data.id}`;
      })
      .catch(err => {
        alert(`Failed to create session: ${err.message}`);
      });
  }
}

function initializeChatView() {
  const messagesEl = document.getElementById('messages');
  const inputEl = document.getElementById('input');
  const sendBtn = document.getElementById('send-btn');
  const statusDot = document.getElementById('status-dot');
  const statusText = document.getElementById('status-text');
  const sessionId = window.currentSessionId;
  let turn = 0;

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

  function setStatus(state, text) {
    statusDot.className = state;
    statusText.textContent = text;
  }

  function sendMessage() {
    const query = inputEl.value.trim();
    if (!query) return;
    const thisTurn = turn;
    turn++;
    const userDiv = document.createElement('div');
    userDiv.className = 'message user';
    userDiv.id = `user-${thisTurn}`;
    userDiv.textContent = query;
    messagesEl.appendChild(userDiv);
    messagesEl.scrollTop = messagesEl.scrollHeight;
    inputEl.value = '';
    inputEl.style.height = 'auto';
    sendBtn.disabled = true;
    setStatus('connected', 'thinking...');
    const responseDiv = document.createElement('div');
    responseDiv.className = 'message assistant typing';
    responseDiv.id = `response-${thisTurn}`;
    responseDiv.innerHTML = '<span></span><span></span><span></span>';
    messagesEl.appendChild(responseDiv);
    messagesEl.scrollTop = messagesEl.scrollHeight;
    streamResponse(query, thisTurn);
  }

  function streamResponse(query, thisTurn) {
    const url = `/api/stream?query=${encodeURIComponent(query)}&session=${sessionId}`;
    const source = new EventSource(url);

    function decodeSSENewlines(html) {
      return html.replace(/\\n/g, '<br>');
    }

    source.addEventListener('session', (e) => {
      setStatus('connected', 'thinking...');
    });

    source.addEventListener('status', (e) => {
      setStatus('connected', 'thinking...');
    });

    source.addEventListener('message', (e) => {
      const responseEl = document.getElementById(`response-${thisTurn}`);
      if (!responseEl) return;
      responseEl.classList.remove('typing');
      responseEl.innerHTML = decodeSSENewlines(e.data);
      messagesEl.scrollTop = messagesEl.scrollHeight;
    });

    source.addEventListener('error', (e) => {
      const responseEl = document.getElementById(`response-${thisTurn}`);
      if (!responseEl) return;
      responseEl.classList.remove('typing');
      responseEl.innerHTML = decodeSSENewlines(e.data);
      sendBtn.disabled = false;
      setStatus('error', 'error');
    });

    source.addEventListener('tool', (e) => {
      const data = JSON.parse(e.data);
      const responseEl = document.getElementById(`response-${thisTurn}`);
      if (!responseEl) return;
      responseEl.classList.add('typing');
    });

    source.addEventListener('done', (e) => {
      source.close();
      sendBtn.disabled = false;
      setStatus('', 'disconnected');
    });

    source.onerror = () => {
      source.close();
      const el = document.getElementById(`response-${thisTurn}`);
      if (el) {
        el.classList.remove('typing');
        el.innerHTML = `<span class='error'>Connection error</span>`;
      }
      sendBtn.disabled = false;
      setStatus('error', 'error');
    };
  }
}
