# M2 设计:Web 逐手回放(本轮只做回放)

> 日期:2026-06-18
> 范围:把 SQLite 里的局/手/动作(含内心戏)用 Web 页面可视化,支持逐手、逐动作回放,展开模型 reasoning。
> 不含:WebSocket 实时直播、排行榜页(排行榜已有 CLI;真要再加 M3)、6-max。

---

## 1. 目标

浏览器打开 `http://localhost:8080`,看到:
1. **局列表页** `/` —— 列出所有 game(时间、双方 label、赢家、手数),点进去
2. **回放页** `/game/{id}` —— 左右两个玩家面板(底牌/筹码/位置),中间公共牌区,下方时间轴(每手一行,点展开 → 该手每个动作一个卡片,含 reasoning + hs/eq/bluff 气泡)

核心:**让模型的内心戏摊在 UI 上**,一眼看出"它在想什么"。

## 2. 后端 API(JSON)

`internal/server/server.go`:`net/http` 起服务,路由:

| 路由 | 返回 |
|---|---|
| `GET /api/games` | 局列表 `[{id, p1_label, p2_label, winner_label, hands_played, started_at, p1_final, p2_final}]` |
| `GET /api/games/{id}` | 单局明细:元信息 + 所有手牌 `[{hand_index, button_seat, folded, pot, winner_label, p1_hole, p2_hole, community, actions:[{seq, street, seat, player_label, action_type, amount, reasoning, hs, eq, bluff}]}]` |

- 用 `store.Store` 的查询方法。`/api/games` 用一个新方法 `ListGames()`,回放页用 `GetGame(id)` 返回完整树。
- 静态文件:`/` 路径由 `http.FileServer` 服务 `web/` 目录(`index.html` / `app.js` / `style.css`)。
- 路径参数 `{id}` 用标准库 `strings.TrimPrefix` 解析(不引 gorilla/mux,极简)。

## 3. 数据查询层(store 包扩展)

需要给 `store.Store` 加两个方法:

```go
type GameSummary struct {
    ID          int64
    P1Label     string
    P2Label     string
    WinnerLabel string  // 空串 = 平局
    HandsPlayed int
    StartedAt   string  // RFC3339
    P1Final     int
    P2Final     int
}

func (s *Store) ListGames(limit int) ([]GameSummary, error)

type GameDetail struct {
    GameSummary
    P1PlayerID int64
    P2PlayerID int64
    Hands      []HandDetail
}

type HandDetail struct {
    HandIndex  int
    ButtonSeat int
    Folded     bool
    Pot        int
    WinnerLabel string
    P1Hole     string
    P2Hole     string
    Community  string
    Actions    []ActionDetail
}

type ActionDetail struct {
    Seq          int
    Street       string
    Seat         int
    PlayerLabel  string
    ActionType   string
    Amount       int
    Reasoning    string  // 空 = 规则 bot
    HandStrength float64 // 0 = 无
    EstEquity    float64
    IsBluffing   bool
    HasReport    bool
}

func (s *Store) GetGame(gameID int64) (*GameDetail, error)
```

SQL JOIN 一次拉完整树(`games ⨝ hands ⨝ actions ⨝ players`),Go 侧组装成嵌套结构。

## 4. 前端(原生 HTML/CSS/JS)

`web/index.html` —— 局列表。fetch `/api/games`,渲染表格,每行链接到 `/game/{id}`(用 hash route `#/game/{id}` 简化,单页应用)。

`web/game.html`(或同一 `index.html` + hash route)—— 回放页:
- 顶部:双方 label + 最终筹码 + 赢家徽章
- 左右:两个玩家面板(头像位、底牌大字、筹码、位置 SB/BB)
- 中间:公共牌区(preflop 空、flop 3 张、turn 4、river 5)
- 下方:**时间轴** = 所有手牌横向滚动条,点击某手加载到主视图;每手下面是 actions 流,每个 action 一张卡(动作图标 + reasoning 文字 + hs/eq/bluff 小气泡)
- 控件:上一手 / 下一手 / 上一动作 / 下一动作(键盘 ← →)

风格:深色背景、扑克绿牌桌、reasoning 气泡用对比色(LLM 蓝、规则 bot 灰)。**不上框架**,fetch + DOM 操作。CSS 用 grid/flex,无依赖。

## 5. 单测覆盖

- **store**:ListGames(空/多条)、GetGame(树结构正确、空手、含/不含内心戏的动作)。临时 DB。
- **server**:用 `httptest` 起 server,测 `/api/games` 与 `/api/games/{id}` 返回正确 JSON、404、空列表。前端不写测试(手动验证)。

## 6. 任务分解

1. **Task 1**:store 加 ListGames + GetGame + HandDetail/ActionDetail 类型 + 单测
2. **Task 2**:server 包 —— net/http + 路由 + JSON 序列化 + httptest 单测
3. **Task 3**:前端 —— index.html(局列表)+ game.html(回放)+ app.js + style.css
4. **Task 4**:CLI `serve` 子命令(--addr default :8080)+ 静态文件服务
5. **Task 5**:收尾验收 —— 用之前 10 手的 DB 启动服务,浏览器打开看回放

## 7. 不在本步范围

- WebSocket 实时直播(要 match 包加 hook,留 M3)
- 排行榜页(CLI 已能查;真要做时再加 `/api/leaderboard` 与页面)
- 多人桌、复式发牌
- 决策质量评分可视化(雷达图、校准曲线)
