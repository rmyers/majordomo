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
    setStatus('connected', 'connecting...');
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
    fetch(url, {
      method: 'GET',
      headers: { 'Accept': 'text/event-stream' }
    })
      .then(response => {
        if (!response.ok) {
          throw new Error(`Server error: ${response.status}`);
        }
        setStatus('connected', 'thinking...');
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        let currentEventType = '';
        function readChunk() {
          reader.read().then(({ done, value }) => {
            if (done) {
              sendBtn.disabled = false;
              setStatus('', 'disconnected');
              return;
            }
            buffer += decoder.decode(value, { stream: true });
            const lines = buffer.split('\n');
            buffer = lines.pop() || '';
            for (const line of lines) {
              const trimmed = line.trim();
              if (!trimmed) continue;
              if (trimmed.startsWith('event: ')) {
                currentEventType = trimmed.slice(7).trim();
                continue;
              }
              if (trimmed.startsWith('data: ')) {
                const data = trimmed.slice(6);
                if (data === '[DONE]') {
                  setStatus('', 'disconnected');
                  continue;
                }
                try {
                  const parsed = JSON.parse(data);
                  parsed.type = currentEventType;
                  handleServerEvent(parsed, thisTurn);
                } catch (e) {
                  // Not JSON, ignore
                }
              }
            }
            readChunk();
          });
        }
        readChunk();
      })
      .catch(err => {
        const el = document.getElementById(`response-${thisTurn}`);
        if (el) {
          el.classList.remove('typing');
          el.textContent = `Error: ${err.message}`;
        }
        sendBtn.disabled = false;
        setStatus('error', 'error');
      });
  }

  function handleServerEvent(event, thisTurn) {
    const responseEl = document.getElementById(`response-${thisTurn}`);
    if (!responseEl) return;
    if (event.type === 'message' || event.content) {
      const content = event.content || (event.message && event.message.content) || '';
      if (content) {
        responseEl.classList.remove('typing');
        responseEl.textContent = content;
        messagesEl.scrollTop = messagesEl.scrollHeight;
      }
    }
    if (event.type === 'error') {
      responseEl.classList.remove('typing');
      responseEl.textContent = `Error: ${event.message || 'Unknown error'}`;
      sendBtn.disabled = false;
      setStatus('error', 'error');
    }
  }
}
