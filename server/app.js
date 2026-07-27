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
  let toolElements = {};

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


  function createToolSection(name, result, isErr) {
    const toolSection = document.createElement('div');
    toolSection.className = 'tool-section';
    toolSection.setAttribute('data-tool', name);

    const toolHeader = document.createElement('div');
    toolHeader.className = 'tool-header';
    toolHeader.innerHTML = `<span class="arrow">▶</span> ${name}`;

    const toolBody = document.createElement('div');
    toolBody.className = 'tool-body' + (isErr ? ' error' : '');
    if (isErr || result === 'running...') {
      toolBody.classList.add('open');
    }
    toolBody.textContent = result === 'running...' ? 'running...' : result;

    toolHeader.addEventListener('click', () => {
      const arrow = toolHeader.querySelector('.arrow');
      const isOpen = toolBody.classList.toggle('open');
      arrow.classList.toggle('open', isOpen);
    });

    toolSection.appendChild(toolHeader);
    toolSection.appendChild(toolBody);
    return toolSection;
  }

  function decodeSSENewlines(html) {
    return html.replace(/\\n/g, '<br>');
  }

  function sendMessage() {
    const query = inputEl.value.trim();
    if (!query) return;
    const thisTurn = turn;
    turn++;
    toolElements = {};
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

  function createToolSection(callId, name, args) {
    const details = document.createElement('details');
    details.className = 'tool-section';
    details.id = `tool-${callId}`;

    const summary = document.createElement('summary');
    summary.className = 'tool-header';
    summary.textContent = name;

    const argsPre = document.createElement('pre');
    argsPre.className = 'tool-body';
    argsPre.id = `tool-args-${callId}`;
    argsPre.textContent = args || '';

    const resultPre = document.createElement('pre');
    resultPre.className = 'tool-body';
    resultPre.id = `tool-result-${callId}`;
    resultPre.textContent = '';

    details.appendChild(summary);
    details.appendChild(argsPre);
    details.appendChild(resultPre);

    return details;
  }

  function decodeSSENewlines(html) {
    return html.replace(/\\n/g, '<br>');
  }

  function streamResponse(query, thisTurn) {
    const url = `/api/stream?query=${encodeURIComponent(query)}&session=${sessionId}`;
    const source = new EventSource(url);

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

      const callId = data.callId;
      if (!callId || toolElements[callId]) return;

      const toolDiv = createToolSection(callId, data.name, data.args || '');
      toolElements[callId] = { resultBody: document.getElementById(`tool-result-${callId}`) };
      responseEl.appendChild(toolDiv);
      messagesEl.scrollTop = messagesEl.scrollHeight;
    });

    source.addEventListener('tool_result', (e) => {
      const data = JSON.parse(e.data);
      const callId = data.callId;
      const entry = toolElements[callId];
      if (!entry) return;

      const resultBody = entry.resultBody;
      resultBody.textContent = data.output || '';
      if (data.error) {
        resultBody.classList.add('error');
      }
      messagesEl.scrollTop = messagesEl.scrollHeight;
    });

    source.addEventListener('done', (e) => {
      source.close();
      const responseEl = document.getElementById(`response-${thisTurn}`);
      if (responseEl && responseEl.textContent.trim() === '' && !responseEl.querySelector('.tool-section')) {
        responseEl.remove();
      }
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
