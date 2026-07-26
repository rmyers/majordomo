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
      responseEl.classList.remove('typing');

      const existingTool = responseEl.querySelector(`.tool-section[data-tool="${data.name}"]`);
      if (existingTool) return;

      const toolSection = createToolSection(data.name, 'running...', false);
      const toolBody = toolSection.querySelector('.tool-body');
      if (data.html) toolBody.innerHTML = decodeSSENewlines(data.html);
      responseEl.appendChild(toolSection);
      messagesEl.scrollTop = messagesEl.scrollHeight;
    });

    source.addEventListener('tool_result', (e) => {
      const data = JSON.parse(e.data);
      const responseEl = document.getElementById(`response-${thisTurn}`);
      if (!responseEl) return;

      const toolSection = responseEl.querySelector(`.tool-section[data-tool="${data.name}"]`);
      if (!toolSection) return;

      const toolBody = toolSection.querySelector('.tool-body');
      const arrow = toolSection.querySelector('.arrow');

      const isErr = data.output !== 'running...' && (data.output.includes('error:') || data.output.startsWith('read ') || data.output.startsWith('write ') || data.output.includes('command failed') || data.output.includes('no change:'));

      if (data.html) {
        toolBody.innerHTML = decodeSSENewlines(data.html);
      } else {
        toolBody.textContent = data.output;
      }

      if (isErr) {
        toolBody.className = 'tool-body error open';
        arrow.textContent = '▶';
        arrow.classList.remove('open');
      } else {
        toolBody.className = 'tool-body open';
        arrow.textContent = '▶';
        arrow.classList.remove('open');
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
