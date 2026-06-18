// PokerMind 回放页前端。原生 JS,hash route 单页。
// 两个视图:#/  = 局列表  #/game/{id} = 单局回放

const app = document.getElementById('app');

window.addEventListener('hashchange', route);
window.addEventListener('DOMContentLoaded', route);

function route() {
  const hash = location.hash || '#/';
  const m = hash.match(/^#\/game\/(\d+)$/);
  if (m) {
    renderGameReplay(parseInt(m[1], 10));
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
      const winnerBadge = g.is_draw
        ? '<span class="winner-badge draw">平局</span>'
        : `<span class="winner-badge">${escapeHtml(g.winner_label)} 赢</span>`;
      const started = new Date(g.started_at).toLocaleString('zh-CN', { hour12: false });
      return `
        <a class="game-row" href="#/game/${g.id}">
          <span class="game-id">#${g.id}</span>
          <div class="player">
            <span class="player-label">${escapeHtml(g.p1_label)}</span>
            <span class="player-chips">${g.p1_final}</span>
          </div>
          <span class="vs">vs</span>
          <div class="player">
            <span class="player-label">${escapeHtml(g.p2_label)}</span>
            <span class="player-chips">${g.p2_final}</span>
          </div>
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

  const winnerBadge = g.is_draw
    ? '<span class="winner-badge draw">平局</span>'
    : `<span class="winner-badge">${escapeHtml(g.winner_label)} 赢</span>`;

  // 时间轴
  const handChips = g.hands.map(h => {
    const cls = h.hand_index === currentHandIndex ? 'hand-chip current' : 'hand-chip';
    const winnerTag = h.is_draw ? '平' : (h.winner_label === g.p1_label ? 'P1' : 'P2');
    return `<span class="${cls}" data-hand="${h.hand_index}" title="手 ${h.hand_index}">${h.hand_index}${h.folded ? '·F' : '·S'}</span>`;
  }).join('');

  const hand = g.hands.find(h => h.hand_index === currentHandIndex) || g.hands[0];

  // 双玩家面板。button_seat 0 表示 seat0(P1)是按钮(SB)
  const p1IsButton = hand.button_seat === 0;
  const p1Class = ['seat', 'seat-1'];
  const p2Class = ['seat', 'seat-2'];

  app.innerHTML = `
    <div class="replay-header">
      <a href="#/" class="back-link">← 返回列表</a>
      <h2>${escapeHtml(g.p1_label)} vs ${escapeHtml(g.p2_label)}</h2>
      ${winnerBadge}
      <span style="color:var(--text-dim);font-size:12px">${g.hands_played} 手 · ${escapeHtml(g.started_at)}</span>
    </div>

    <div class="table">
      <div class="street-badge">${escapeHtml(handStreetLabel(hand))} · 手 ${hand.hand_index}</div>

      <div class="${p1Class.join(' ')}">
        <span class="seat-name">${escapeHtml(g.p1_label)}</span>
        <span class="seat-pos">${p1IsButton ? 'SB · 按钮' : 'BB'}</span>
        <span class="seat-cards">${renderCards(hand.p1_hole)}</span>
        <span class="seat-stack">stack ${g.p1_final}</span>
      </div>

      <div class="community-area">
        <div class="community-cards">${renderCards(hand.community)}</div>
        <div class="pot">底池 <span class="amount">${hand.pot}</span></div>
      </div>

      <div class="${p2Class.join(' ')}">
        <span class="seat-name">${escapeHtml(g.p2_label)}</span>
        <span class="seat-pos">${p1IsButton ? 'BB' : 'SB · 按钮'}</span>
        <span class="seat-cards">${renderCards(hand.p2_hole)}</span>
        <span class="seat-stack">stack ${g.p2_final}</span>
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

  // 绑定时间轴点击
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

  // 键盘 ← →
  document.onkeydown = (e) => {
    if (e.key === 'ArrowLeft' && currentHandIndex > 1) {
      currentHandIndex--; renderReplay();
    } else if (e.key === 'ArrowRight' && currentHandIndex < totalHands) {
      currentHandIndex++; renderReplay();
    }
  };
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
