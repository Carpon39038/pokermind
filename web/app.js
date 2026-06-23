// PokerMind 回放页前端。原生 JS,hash route 单页。
// 两个视图:#/  = 局列表  #/game/{id} = 单局回放

const app = document.getElementById('app');

window.addEventListener('hashchange', route);
window.addEventListener('DOMContentLoaded', route);

function route() {
  const hash = location.hash || '#/';
  let m;
  if ((m = hash.match(/^#\/game\/(\d+)$/))) {
    renderGameReplay(parseInt(m[1], 10));
  } else if (hash === '#/providers') {
    renderProviders();
  } else if (hash === '#/live') {
    renderLiveConfig();
  } else if ((m = hash.match(/^#\/live\/(.+)$/))) {
    renderLiveWatch(m[1]);
  } else {
    renderGameList();
  }
}

// 花色字符与颜色映射
const SUIT_CHAR = { s: '♠', h: '♥', d: '♦', c: '♣' };
const RANK_DISPLAY = { 14: 'A', 13: 'K', 12: 'Q', 11: 'J', 10: 'T' };

// 把 "Ac Kh" 渲染成具象扑克牌组件(白底圆角 + 左上角点数花色 + 中央大花色)
// 空串 → 一张牌背占位(对手未摊牌)
function renderCards(str) {
  if (!str || !str.trim()) {
    return '<div class="cards-row empty">未摊牌</div>';
  }
  const cards = str.trim().split(/\s+/).map(renderCard).join('');
  return `<div class="cards-row">${cards}</div>`;
}

// 渲染固定数量的牌背(用于对手底牌未公开时)
function renderCardBacks(n) {
  const backs = Array.from({ length: n }, () => '<div class="card back"></div>').join('');
  return `<div class="cards-row">${backs}</div>`;
}

// 单张牌:token 如 "As" → A 黑桃
function renderCard(token) {
  if (token.length < 2) return token;
  const rankChar = token.slice(0, -1);     // "A" / "K" / "10"(注意 T 不是 10)
  const suitChar = token[token.length - 1]; // "s"/"h"/"d"/"c"
  // 数据里 T 表示 10,显示还原成 10
  const rankDisplay = (rankChar === 'T') ? '10' : rankChar;
  const suitSymbol = SUIT_CHAR[suitChar] || suitChar;
  const isRed = (suitChar === 'h' || suitChar === 'd');
  const colorCls = isRed ? 'red' : 'black';
  return `
    <div class="card ${colorCls}">
      <div class="corner">
        <span class="rank">${escapeHtml(rankDisplay)}</span>
        <span class="suit">${suitSymbol}</span>
      </div>
      <div class="pip">${suitSymbol}</div>
    </div>`;
}

async function fetchJSON(url) {
  const res = await fetch(url);
  if (!res.ok) {
    throw new Error(`${res.status} ${res.statusText} — ${url}`);
  }
  return res.json();
}

function escapeHtml(s) {
  if (s == null) return '';
  return String(s)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
}

// ============ 局列表 ============
async function renderGameList() {
  app.innerHTML = '<p class="loading">加载局列表…</p>';
  try {
    const games = await fetchJSON('/api/games?limit=100');
    if (!games || games.length === 0) {
      app.innerHTML = '<div class="empty">还没有对局记录。<br>先跑 <code>pokermind match --p1 … --p2 …</code> 产生数据。</div>';
      return;
    }
    const rows = games.map(g => {
      const started = new Date(g.started_at).toLocaleString('zh-CN', { hour12: false });
      // 玩家列表:每个 player 一段 label + chips;赢家加粗
      const playerCells = (g.players || []).map(p => {
        const cls = p.is_winner ? 'player player-winner' : 'player';
        return `<div class="${cls}"><span class="player-label">${escapeHtml(p.label)}</span><span class="player-chips">${p.final_chips}</span></div>`;
      }).join('<span class="vs">vs</span>');
      const winnerBadge = g.is_draw
        ? '<span class="winner-badge draw">平局</span>'
        : `<span class="winner-badge">有赢家</span>`;
      const seatCount = g.num_seats || (g.players || []).length;
      return `
        <a class="game-row" href="#/game/${g.id}">
          <span class="game-id">#${g.id} ·${seatCount}max</span>
          <span class="players-cell">${playerCells}</span>
          <span>${winnerBadge}</span>
          <span class="hand-count">${g.hands_played} 手<br>${started}</span>
        </a>`;
    }).join('');
    app.innerHTML = `
      <h2 style="margin-top:0">局列表</h2>
      <div class="game-list">${rows}</div>
    `;
  } catch (e) {
    app.innerHTML = `<div class="error">加载失败:${escapeHtml(e.message)}</div>`;
  }
}

// ============ 单局回放 ============
let currentGame = null;
let currentHandIndex = 0; // 1-based

async function renderGameReplay(gameId) {
  app.innerHTML = '<p class="loading">加载对局…</p>';
  try {
    const g = await fetchJSON(`/api/games/${gameId}`);
    currentGame = g;
    currentHandIndex = g.hands.length > 0 ? 1 : 0;
    renderReplay();
  } catch (e) {
    app.innerHTML = `<div class="error">加载失败:${escapeHtml(e.message)}</div>`;
  }
}

function renderReplay() {
  const g = currentGame;
  if (!g) return;
  const totalHands = g.hands.length;
  const players = g.players || [];

  // 赢家徽章:列出所有 is_winner 的 player label
  const winnerLabels = players.filter(p => p.is_winner).map(p => p.label);
  const winnerBadge = g.is_draw || winnerLabels.length === 0
    ? '<span class="winner-badge draw">平局</span>'
    : `<span class="winner-badge">${winnerLabels.map(escapeHtml).join(' / ')} 赢</span>`;

  // 时间轴
  const handChips = g.hands.map(h => {
    const cls = h.hand_index === currentHandIndex ? 'hand-chip current' : 'hand-chip';
    return `<span class="${cls}" data-hand="${h.hand_index}" title="手 ${h.hand_index}">${h.hand_index}${h.folded ? '·F' : '·S'}</span>`;
  }).join('');

  const hand = g.hands.find(h => h.hand_index === currentHandIndex) || g.hands[0];
  const seatHtml = renderSeats(players, hand);

  // 标题:列出所有玩家 label(N=2 紧凑,N>2 时省略中间)
  const titleLabels = players.length <= 3
    ? players.map(p => escapeHtml(p.label)).join(' vs ')
    : `${escapeHtml(players[0].label)} vs ... vs ${escapeHtml(players[players.length-1].label)} (${players.length}max)`;

  app.innerHTML = `
    <div class="replay-header">
      <a href="#/" class="back-link">← 返回列表</a>
      <h2>${titleLabels}</h2>
      ${winnerBadge}
      <span style="color:var(--text-dim);font-size:12px">${g.hands_played} 手 · ${escapeHtml(g.started_at)}</span>
    </div>

    <div class="table table-${players.length}">
      <div class="street-badge">${escapeHtml(handStreetLabel(hand))} · 手 ${hand.hand_index}</div>
      <div class="seats-ring">${seatHtml}</div>
      <div class="community-area">
        <div class="community-cards">${renderCards(hand.community)}</div>
        <div class="pot">底池 <span class="amount">${hand.pot}</span></div>
      </div>
    </div>

    <div class="timeline-wrap">
      <div class="timeline-header">
        <h3>时间轴</h3>
        <div class="timeline-nav">
          <button id="prev-hand">← 上一手</button>
          <button id="next-hand">下一手 →</button>
          <span style="color:var(--text-dim);font-size:12px;align-self:center">
            ${currentHandIndex} / ${totalHands}
          </span>
        </div>
      </div>
      <div class="timeline" id="timeline">${handChips}</div>
    </div>

    <div class="actions-stream">
      ${renderActions(hand, g)}
    </div>
  `;

  document.querySelectorAll('.hand-chip').forEach(el => {
    el.addEventListener('click', () => {
      currentHandIndex = parseInt(el.dataset.hand, 10);
      renderReplay();
    });
  });
  document.getElementById('prev-hand').addEventListener('click', () => {
    if (currentHandIndex > 1) { currentHandIndex--; renderReplay(); }
  });
  document.getElementById('next-hand').addEventListener('click', () => {
    if (currentHandIndex < totalHands) { currentHandIndex++; renderReplay(); }
  });

  document.onkeydown = (e) => {
    if (e.key === 'ArrowLeft' && currentHandIndex > 1) {
      currentHandIndex--; renderReplay();
    } else if (e.key === 'ArrowRight' && currentHandIndex < totalHands) {
      currentHandIndex++; renderReplay();
    }
  };
}

// renderSeats 渲染所有 seat 围绕牌桌。N=2 左右,N>=3 上方/侧边环绕。
// 每个 seat 显示 label/位置(SB/BB/BTN/UTG)/底牌/最终筹码。
function renderSeats(players, hand) {
  const n = players.length;
  // player_holes 是 seat-indexed 数组
  const holes = hand.player_holes || [];
  return players.map((p, i) => {
    const isButton = hand.button_seat === i;
    let posTag = 'UTG';
    if (n === 2) {
      posTag = isButton ? 'SB · 按钮' : 'BB';
    } else {
      if (isButton) posTag = 'BTN';
      else if (i === (hand.button_seat + 1) % n) posTag = 'SB';
      else if (i === (hand.button_seat + 2) % n) posTag = 'BB';
    }
    const hole = holes[i] || '';
    const winnerCls = p.is_winner ? ' seat-winner' : '';
    return `
      <div class="seat seat-${i} seat-pos-${i}${winnerCls}" data-seat="${i}">
        <span class="seat-name">${escapeHtml(p.label)}</span>
        <span class="seat-pos">${posTag}</span>
        <span class="seat-cards">${renderCards(hole)}</span>
        <span class="seat-stack">stack ${p.final_chips}</span>
      </div>`;
  }).join('');
}

function handStreetLabel(hand) {
  if (!hand.folded && hand.community) {
    const n = hand.community.trim().split(/\s+/).filter(Boolean).length;
    if (n >= 5) return 'river';
    if (n === 4) return 'turn';
    if (n === 3) return 'flop';
  }
  return hand.folded ? '结束' : 'preflop';
}

function renderActions(hand, g) {
  if (!hand.actions || hand.actions.length === 0) {
    return '<div class="empty" style="padding:16px">这手没有动作记录(可能因弃牌前直接结束)。</div>';
  }
  return hand.actions.map(a => {
    const isLLM = a.has_report;
    const cls = isLLM ? 'action-card llm' : 'action-card rulebot';
    const typeCls = `action-type ${escapeHtml(a.action_type)}`;
    const amountStr = a.action_type === 'raise' ? ` → ${a.amount}` : '';
    const playerTag = isLLM ? 'LLM' : 'rulebot';

    let metricsHtml = '';
    if (isLLM) {
      const metrics = [];
      metrics.push(`<span class="metric">牌力 <strong>${(a.hand_strength * 100).toFixed(0)}%</strong></span>`);
      metrics.push(`<span class="metric">自报胜率 <strong>${(a.estimated_equity * 100).toFixed(0)}%</strong></span>`);
      if (a.is_bluffing) {
        metrics.push(`<span class="metric bluff">⚡ 诈唬</span>`);
      }
      metricsHtml = `<div class="metrics">${metrics.join('')}</div>`;
    }

    const reasoningHtml = isLLM
      ? `<div class="reasoning">${escapeHtml(a.reasoning)}</div>`
      : `<div class="reasoning empty">(规则 bot,无内心戏)</div>`;

    return `
      <div class="${cls}">
        <div class="action-meta">
          <span class="action-actor">${escapeHtml(a.player_label)}</span>
          <span style="color:var(--text-dim)">${escapeHtml(a.street)} · ${playerTag}</span>
          <span class="${typeCls}">${escapeHtml(a.action_type)}${amountStr}</span>
        </div>
        <div class="action-body">
          ${reasoningHtml}
          ${metricsHtml}
        </div>
      </div>
    `;
  }).join('');
}

// ============ providers 配置页 ============
async function renderProviders() {
  app.innerHTML = `
    <section class="page">
      <h2 style="margin-top:0">LLM Providers</h2>
      <table id="prov-table">
        <thead><tr><th>name</th><th>kind</th><th>base_url</th><th>api_key</th><th></th></tr></thead>
        <tbody></tbody>
      </table>
      <h3>+ 新增 / 编辑</h3>
      <form id="prov-form">
        <input name="name" placeholder="name (unique)" required>
        <select name="kind"><option value="openai">openai</option><option value="anthropic">anthropic</option></select>
        <input name="base_url" placeholder="https://..." required>
        <input name="api_key" placeholder="api_key (留空=不改;新建必填)">
        <button type="submit">保存</button>
      </form>
      <p class="hint">kind=<code>openai</code>:DeepSeek/GLM/Qwen 等 OpenAI 兼容;kind=<code>anthropic</code>:Claude。base_url 不要带末尾 /。</p>
    </section>
  `;
  await refreshProvidersTable();
  document.getElementById('prov-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const fd = new FormData(e.target);
    const body = Object.fromEntries(fd.entries());
    const r = await fetch('/api/providers', {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(body),
    });
    if (!r.ok) { alert(await r.text()); return; }
    e.target.reset();
    await refreshProvidersTable();
  });
}

async function refreshProvidersTable() {
  const tbody = document.querySelector('#prov-table tbody');
  if (!tbody) return;
  const r = await fetch('/api/providers');
  const list = await r.json();
  tbody.innerHTML = (list || []).map(p => `
    <tr>
      <td>${escapeHtml(p.name)}</td>
      <td>${escapeHtml(p.kind)}</td>
      <td>${escapeHtml(p.base_url)}</td>
      <td><code>${escapeHtml(p.api_key)}</code></td>
      <td><button data-name="${escapeHtml(p.name)}" class="del">删</button></td>
    </tr>
  `).join('');
  tbody.querySelectorAll('.del').forEach(btn => {
    btn.onclick = async () => {
      if (!confirm(`删除 ${btn.dataset.name}?`)) return;
      const r = await fetch('/api/providers/' + encodeURIComponent(btn.dataset.name), {method: 'DELETE'});
      if (!r.ok) { alert(await r.text()); return; }
      await refreshProvidersTable();
    };
  });
}

// ============ live 配置页 ============
async function renderLiveConfig() {
  let provs = [];
  try {
    const r = await fetch('/api/providers');
    provs = await r.json();
  } catch (e) { /* empty */ }
  const optStr = (provs || []).map(p => `<option value="${escapeHtml(p.name)}">${escapeHtml(p.name)} (${escapeHtml(p.kind)})</option>`).join('');

  app.innerHTML = `
    <section class="page">
      <h2 style="margin-top:0">现场对战</h2>
      <div id="live-status"></div>
      <form id="live-form">
        <div class="live-form-row">
          <label>座位数 <select name="n" id="seat-n">
            <option>2</option><option>3</option><option>4</option><option>5</option><option>6</option>
          </select></label>
          <label>手数 <input name="hands" value="20" type="number" min="1"></label>
          <label>seed <input name="seed" value="" type="number" placeholder="随机"></label>
          <label>SB <input name="sb" value="5" type="number"></label>
          <label>BB <input name="bb" value="10" type="number"></label>
          <label>起手筹码 <input name="starting_stack" value="1000" type="number"></label>
        </div>
        <table id="seat-table"><thead><tr><th>seat</th><th>provider</th><th>model</th></tr></thead><tbody></tbody></table>
        <button type="submit">开始对局</button>
      </form>
    </section>
  `;

  const seatN = document.getElementById('seat-n');
  const tbody = document.querySelector('#seat-table tbody');
  function rebuildSeats() {
    const n = parseInt(seatN.value, 10);
    tbody.innerHTML = Array.from({length: n}, (_, i) => `
      <tr>
        <td>${i}</td>
        <td><select name="seat_${i}_provider">${optStr}</select></td>
        <td><input name="seat_${i}_model" placeholder="model name" required></td>
      </tr>
    `).join('');
  }
  seatN.addEventListener('change', rebuildSeats);
  rebuildSeats();

  try {
    const stReq = await fetch('/api/matches/current');
    const st = await stReq.json();
    if (st.running) {
      document.getElementById('live-status').innerHTML = `
        <p class="hint">⚠️ 当前有对局在跑:<a href="#/live/${escapeHtml(st.match_id)}">前往观战 →</a></p>
      `;
    }
  } catch (e) { /* ignore */ }

  document.getElementById('live-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const fd = new FormData(e.target);
    const n = parseInt(fd.get('n'), 10);
    const seats = [];
    for (let i = 0; i < n; i++) {
      seats.push({provider: fd.get(`seat_${i}_provider`), model: fd.get(`seat_${i}_model`)});
    }
    const body = {
      seats, hands: parseInt(fd.get('hands'), 10),
      sb: parseInt(fd.get('sb'), 10), bb: parseInt(fd.get('bb'), 10),
      starting_stack: parseInt(fd.get('starting_stack'), 10),
    };
    if (fd.get('seed')) body.seed = parseInt(fd.get('seed'), 10);
    const r = await fetch('/api/matches', {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(body),
    });
    if (!r.ok) { alert(await r.text()); return; }
    const res = await r.json();
    location.hash = '#/live/' + res.match_id;
  });
}

// ============ live 观战页 ============
function renderLiveWatch(matchID) {
  app.innerHTML = `
    <section class="page">
      <header class="live-header">
        <h2 style="margin:0">现场观战 <small id="match-progress"></small></h2>
        <button id="stop-btn">停止对局</button>
      </header>
      <div class="table" id="live-table">
        <div class="street-badge" id="street-badge">等待发牌…</div>
        <div class="seats-ring" id="seats-ring"></div>
        <div class="community-area">
          <div class="community-cards" id="community"></div>
          <div class="pot">底池 <span class="amount" id="pot">0</span></div>
        </div>
      </div>
      <ul class="action-log" id="log"></ul>
    </section>
  `;

  let totalHands = 0;
  let currentHand = 0;
  let buttonSeat = -1;
  let since = -1; // -1 表示尚未初始化,首次 pollOnce 会跳过 replay
  let pollTimer = null;
  const seats = {};

  document.getElementById('stop-btn').onclick = async () => {
    if (!confirm('停止当前对局?(不会落库)')) return;
    await fetch('/api/matches/current/stop', {method: 'POST'});
  };

  async function startPolling() {
    // 先拉一次全部历史,做两件事:
    // 1) 如果对局未在运行且无事件 → 提示 + 退出
    // 2) 如果在运行,从「最后一个 hand_started」位置开始 replay(避免跳过当前手中间状态),
    //    但更早的事件(match_started、之前手)也要选择性补放(seats 要 init)。
    try {
      const r = await fetch(`/api/matches/current/events?since=0`);
      const data = await r.json();
      if (!data.running && (!data.events || data.events.length === 0)) {
        document.getElementById('log').innerHTML = `<li class="sys">没有正在运行的对局。回 <a href="#/live">#/live</a> 开一局。</li>`;
        return;
      }
      const evs = data.events || [];
      // 先放 match_started(初始化 seats)
      const ms = evs.find(e => e.type === 'match_started');
      if (ms) handleEvent('match_started', ms.payload);
      // 找最后一个 hand_started,从那以后完整 replay(含该 hand_started)
      let lastHSIdx = -1;
      for (let i = evs.length - 1; i >= 0; i--) {
        if (evs[i].type === 'hand_started') { lastHSIdx = i; break; }
      }
      if (lastHSIdx >= 0) {
        for (let i = lastHSIdx; i < evs.length; i++) {
          handleEvent(evs[i].type, evs[i].payload);
        }
      }
      since = data.next_since;
    } catch (e) { /* ignore */ }
    if (pollTimer) clearInterval(pollTimer);
    pollTimer = setInterval(pollOnce, 500);
  }

  async function pollOnce() {
    if (since < 0) return;
    let r;
    try {
      r = await fetch(`/api/matches/current/events?since=${since}`);
    } catch (e) { return; }
    if (!r.ok) return;
    let data;
    try { data = await r.json(); } catch (e) { return; }
    since = data.next_since;
    for (const ev of (data.events || [])) {
      try { handleEvent(ev.type, ev.payload); }
      catch (e) { console.error('handleEvent', ev, e); }
    }
    if (!data.running && (!data.events || data.events.length === 0)) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  function handleEvent(type, d) {
    switch (type) {
      case 'match_started':
        totalHands = d.hands;
        renderTable(d.seats);
        break;
      case 'hand_started':
        currentHand = d.hand;
        buttonSeat = d.button;
        setStreet('preflop');
        document.getElementById('match-progress').textContent = `第 ${d.hand}/${totalHands} 手`;
        document.getElementById('community').innerHTML = '';
        document.getElementById('pot').textContent = '0';
        Object.values(seats).forEach(s => { s.bet = 0; s.folded = false; s.winner = false; s.thinking = false; s.hole = []; s.lastAction = null; });
        appendLog(`<li class="sys">—— 第 ${d.hand} 手开始(Button=seat${d.button})——</li>`);
        refreshView();
        break;
      case 'holes_dealt':
        for (const h of (d.holes || [])) {
          if (seats[h.seat]) seats[h.seat].hole = h.cards;
        }
        refreshView();
        break;
      case 'thinking':
        // 不再切换 seat 状态:thinking 实际上是 PlayHand 内部实时 emit 的,
        // 但 holes_dealt 和 action 是 PlayHand 结束后批量 emit,两者时序对不齐。
        // 改成纯日志,UI 上的「思考中」由 action 之间的间隔自然体现。
        if (seats[d.seat]) {
          appendLog(`<li class="thinking">第 ${currentHand} 手 · seat${d.seat} ${escapeHtml(d.model)} 思考中… (${escapeHtml(d.street)})</li>`);
        }
        break;
      case 'action': {
        const s = seats[d.seat];
        if (!s) break;
        s.thinking = false;
        const hs = (typeof d.hand_strength === 'number') ? d.hand_strength.toFixed(2) : null;
        const eq = (typeof d.estimated_equity === 'number') ? d.estimated_equity.toFixed(2) : null;
        s.lastAction = {
          type: d.action_type,
          amount: d.amount,
          street: d.street,
          reasoning: d.reasoning || '',
          hasReport: !!d.has_report,
          hs, eq,
          isBluff: !!d.is_bluffing,
          ts: Date.now(),
        };
        // 历史 log 仍保留(可选)
        let line = `<strong>seat${d.seat}</strong> <span class="action-type ${escapeHtml(d.action_type)}">${d.action_type.toUpperCase()}</span>`;
        if (d.action_type === 'raise') line += ` → ${d.amount}`;
        if (d.has_report) line += ` <em>“${escapeHtml(d.reasoning || '')}”</em>${hs ? ` <span class="metrics">hs=${hs} eq=${eq}${d.is_bluff ? ' ⚡诈唬' : ''}</span>` : ''}`;
        appendLog(`<li>${line}</li>`);
        if (d.action_type === 'fold') s.folded = true;
        if (d.action_type === 'raise') s.bet = d.amount;
        setStreet(d.street);
        refreshView();
        break;
      }
      case 'community_update':
        document.getElementById('community').innerHTML = (d.community || []).map(renderCard).join('');
        setStreet(d.street);
        break;
      case 'hand_finished':
        if (d.pot) document.getElementById('pot').textContent = d.pot;
        if (d.community && d.community.length > 0) {
          document.getElementById('community').innerHTML = d.community.map(renderCard).join('');
        }
        for (const seat of d.winners) {
          if (seats[seat]) seats[seat].winner = true;
        }
        appendLog(`<li class="sys">第 ${d.hand} 手结束 · 赢家 seat ${d.winners.join(',')} · ${d.folded ? '弃牌' : '摊牌'}</li>`);
        refreshView();
        break;
      case 'match_finished': {
        const summary = d.final_stacks.map((c, i) => `seat${i}=${c}`).join(', ');
        appendLog(`<li class="sys">对局结束 · 赢家 seat ${d.winner_seat} · game_id=${d.game_id}<br>${escapeHtml(summary)}</li>`);
        document.getElementById('stop-btn').disabled = true;
        break;
      }
      case 'error':
        if (d && d.error) appendLog(`<li class="sys">错误:${escapeHtml(d.error)}</li>`);
        break;
    }
  }

  startPolling();

  function setStreet(name) {
    document.getElementById('street-badge').textContent = name;
  }

  function appendLog(html) {
    const log = document.getElementById('log');
    log.insertAdjacentHTML('beforeend', html);
    log.scrollTop = log.scrollHeight;
  }

  function renderTable(seatList) {
    const ring = document.getElementById('seats-ring');
    ring.innerHTML = seatList.map(s => {
      seats[s.seat] = {
        label: `${s.provider}:${s.model}`,
        provider: s.provider, model: s.model,
        stack: 0, bet: 0, hole: [], folded: false, winner: false, thinking: false,
      };
      return `<div class="seat" id="seat-${s.seat}"></div>`;
    }).join('');
    refreshView();
  }

  function refreshView() {
    const n = Object.keys(seats).length;
    for (const [idStr, s] of Object.entries(seats)) {
      const el = document.getElementById('seat-' + idStr);
      if (!el) continue;
      const id = parseInt(idStr, 10);
      el.classList.toggle('active', !!s.thinking);
      el.classList.toggle('seat-winner', !!s.winner);
      el.classList.toggle('folded', !!s.folded);

      // 位置标签
      let posTag = 'UTG';
      if (n === 2) {
        posTag = id === buttonSeat ? 'SB · BTN' : 'BB';
      } else if (buttonSeat >= 0) {
        if (id === buttonSeat) posTag = 'BTN';
        else if (id === (buttonSeat + 1) % n) posTag = 'SB';
        else if (id === (buttonSeat + 2) % n) posTag = 'BB';
      }
      const holeStr = (s.hole || []).join(' ');
      const holeHtml = renderCards(holeStr);

      // 状态行:思考中 > 最后动作 > 空闲
      let statusHtml = '';
      if (s.thinking) {
        statusHtml = `<span class="seat-status thinking">🤔 思考中…</span>`;
      } else if (s.lastAction) {
        const a = s.lastAction;
        const typeTxt = a.type.toUpperCase() + (a.type === 'raise' ? `→${a.amount}` : '');
        const reportTxt = a.hasReport
          ? `<span class="seat-reasoning">“${escapeHtml(a.reasoning)}”</span>${a.hs ? `<span class="metrics">hs=${a.hs} eq=${a.eq}${a.isBluff ? ' ⚡' : ''}</span>` : ''}`
          : '';
        statusHtml = `<span class="seat-status"><span class="action-type ${escapeHtml(a.type)}">${typeTxt}</span></span>${reportTxt}`;
      }

      el.innerHTML = `
        <span class="seat-name">${escapeHtml(s.label)}</span>
        <span class="seat-pos">${posTag}</span>
        <span class="seat-cards">${holeHtml}</span>
        <span class="seat-stack">${s.bet > 0 ? `投入 ${s.bet}` : ''}</span>
        <div class="seat-status-wrap">${statusHtml}</div>
      `;
    }
  }
}
