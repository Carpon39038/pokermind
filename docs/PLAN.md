# PokerMind — 多模型德州扑克擂台（看得见「内心戏」）

> 版本：2026-06-18
> 语言：Go 1.25
> 定位：多个大模型同桌打德州扑克，**把每个模型的「内心戏」（推理、自评手牌强度、诈唬意图）存下来并可视化**，配 ELO 排行榜。
> 参考项目（**仅看思路，不抄一行代码 / 不抄页面**）：`strangeloopcanon/llm-poker`（Python，后端分层思路）、`sgoedecke/ai-poker-arena`（Node，实时牌桌思路）。本项目所有代码与前端页面全部原创。

---

## 0. 这个项目切的缝（为什么值得做）

现存的 LLM 扑克项目（Kaggle Game Arena、PokerBench、上面两个开源项目）有一个共同盲区：

> **它们只记录模型「做了什么动作」，不记录模型「为什么这么做」。** 排行榜是个黑箱——你只知道某模型赢了，不知道它是算对了赔率，还是纯运气拿到好牌。

PokerMind 只做三件事，但把第②件做透（那是别人没有的）：

| # | 做什么 | 是否别人已有 |
|---|---|---|
| ① | 多模型同桌打德州扑克 + 排行榜（ELO） | 已有（Kaggle / 两个开源项目） |
| ② | **把模型的「内心戏」存下来并在回放里可视化** | ❌ 真空，这是差异点 |
| ③ | Web 牌桌 + 逐手回放 | 部分有（sg 有实时牌桌，但无回放、无内心戏） |

**「内心戏」具体指**：每个决策点强制模型结构化自述 —— 它的推理过程、它自评的手牌强度、它自报的胜率估算、它在不在诈唬。回放时把这些摊开，一眼看出「这个模型在想什么」。

---

## 1. 范围校准（明确不做什么）

### 1.1 本次做（第一版闭环）

- 德州扑克引擎：发牌、下注轮、摊牌、定胜负（**自己写**）。
- 模型接入层：抽象 `Player` 接口，各家 provider 用 HTTP adapter 接入（OpenAI 兼容格式打底）。
- 内心戏采集：每个动作连同 `reasoning + self_report` 一起存进 SQLite。
- Web 界面：牌桌可视化 + 思考气泡 + 逐手回放 + ELO 排行榜（**前端自己写，原生 HTML/JS**）。

### 1.2 本次不做（留到以后，避免第一版过重）

- ❌ **决策质量评分**（校准分 / 失误率 / 诈唬自洽度 / LLM 裁判 / 雷达图）—— 依赖蒙特卡洛与裁判模型，重，先把好玩的闭环跑通再说。
- ❌ **复式发牌（duplicate poker）去运气化** —— 架构预留接口，第一版先不做，先跑通。
- ❌ **GTO 最优解对照**。
- ❌ 真实胜率（蒙特卡洛 equity）计算 —— 第一版只存模型「自报」的胜率，不算「真相」。（真相对照属于被砍的决策质量评分层。）
- ❌ 人类玩家加入对局。

> 一句话：**第一版 = 多模型对打 + 内心戏可视化 + ELO 榜。** 评分、去运气、真相对照全部延后。

### 1.3 牌局形式（第一版默认）

- **6-max（6 人桌）** 还是 **Heads-up（单挑）**：第一版先做 **Heads-up**，理由——下注逻辑最简单（无需处理多人 side pot 边池）、ELO 最干净、最快跑通。引擎接口设计成「N 人」通用，6-max 留作 M3 扩展。
- No-Limit Texas Hold'em（无限注德州扑克），标准规则。
- 思考规则：**CoT 全开**，允许模型随便算赔率（已确认）。

---

## 2. 技术栈

| 层 | 选型 | 理由 / 备注 |
|---|---|---|
| 语言 | **Go 1.25** | 已确认。并发跑多局、WebSocket 推流、单二进制部署是强项 |
| 模型接入 | **自定义 `Player` 接口 + 各 provider HTTP adapter** | Go 无 litellm 等价物，自己抽象；各家 API 多兼容 OpenAI 格式，一个 adapter 覆盖大半。**自己写适配层本身就是练手重点** |
| HTTP / 推流 | 标准库 `net/http` + `gorilla/websocket` | 实时把牌局事件推给前端 |
| 存储 | **SQLite**（`modernc.org/sqlite`，纯 Go，免 CGO） | 零运维，回放 + 排行榜都查它。免 CGO 省去交叉编译麻烦 |
| 前端 | **原生 HTML/CSS/JS 单页**（自己写） | 第一版不上框架。牌桌 + 思考气泡 + 时间轴回放 + 排行榜 |
| 牌型比较 | 允许用成熟算法库（如 `chehsunliu/poker`）**或自己写** | 发牌 / 手牌组装 / 业务逻辑全原创；纯粹的「7 选 5 比大小」这种确定性算法可借力，待 M1 决定 |
| 配置 | 环境变量 / `.env`（各家 API key + base url） | key 绝不入库、不入 git |

---

## 3. 架构总览

```
                    ┌─────────────────────────────────────┐
                    │  Engine（确定性，纯 Go，可单测）       │
                    │  ├─ Deck 发牌 / 洗牌                  │
                    │  ├─ HandState 牌局状态机              │
                    │  │   （盲注/下注轮/翻牌转牌河牌/摊牌） │
                    │  ├─ HandEvaluator 牌型比较定胜负      │
                    │  └─ 产出 Event 流（每个动作一个事件）  │
                    └───────────┬─────────────────────────┘
                                │ 需要某玩家决策时
                                ▼
              ┌──────────────────────────────────┐
              │  Player 接口                       │
              │  Decide(ctx, Observation) → Action │
              ├──────────────────────────────────┤
              │  LLMPlayer（实现）                  │
              │   ├─ 拼 prompt（观察 → 文本）        │
              │   ├─ 调 provider adapter（HTTP）     │
              │   └─ 解析结构化 JSON（动作+内心戏）   │
              │      └─ 校验失败重试 N 次            │
              └──────────────────────────────────┘
                                │ Action{ action, amount, self_report }
                                ▼
              ┌──────────────────────────────────┐
              │  Recorder → SQLite                 │
              │   每手每动作落库：动作 + 内心戏 + 结果 │
              └──────────────────────────────────┘
                                │
                                ▼
              ┌──────────────────────────────────┐
              │  Web Server（net/http + ws）        │
              │   ├─ 实时推牌局事件（看直播）         │
              │   ├─ /api/games /api/hands（查回放） │
              │   └─ /api/leaderboard（ELO 榜）      │
              └──────────────────────────────────┘
                                │
                                ▼
                      前端单页（原生 HTML/JS）
                      牌桌 + 思考气泡 + 回放 + 排行榜
```

**设计原则：**
- **Engine 不知道 LLM 的存在**，只认 `Player` 接口。这样引擎可单测（喂假 Player），也能塞规则 bot 当基线。
- **Engine 是确定性的**，发牌用可注入的随机源（带 seed），便于复现某一局 bug。
- **内心戏是 `Action` 的一部分**，从模型返回那一刻就带着，落库时一起存，不会像 slc 那样被丢弃。

---

## 4. 核心数据结构（草案，Go）

```go
// 引擎给模型看的「观察」——模型只能看到这些，看不到别人的底牌
type Observation struct {
    HandID        int
    Street        string   // preflop / flop / turn / river
    HoleCards     []Card   // 自己的底牌
    Community     []Card   // 公共牌
    Pot           int
    ToCall        int      // 跟注需要多少
    MinRaise      int
    MyStack       int
    Position      string   // button / bigblind ...
    History       []string // 本手到目前为止的动作流水（文本）
    Opponents     []OppView// 对手可见信息（筹码、是否已弃牌）
}

// 模型返回的动作 —— 关键：带内心戏
type Action struct {
    Type        string  // fold / call / raise   （check 用 call 且 ToCall=0 表示）
    RaiseAmount int     // Type=raise 时有效
    SelfReport  SelfReport
}

// 内心戏（本项目的灵魂）
type SelfReport struct {
    HandStrength    float64 // 模型自评手牌强度 0-1
    EstimatedEquity float64 // 模型自算胜率 0-1
    IsBluffing      bool    // 模型自述：我在不在诈唬
    Reasoning       string  // 推理过程（给人看的）
}
```

> 注：`true_equity / ground_truth / 校准分` 等「真相对照」字段**本期不做**（§1.2），但 `SelfReport` 已经把模型自述存全，以后加真相对照时直接补一列即可。

---

## 5. 计分（第一版：只做 ELO）

- 一局（match）= Heads-up 打 N 手（如 100 手），按最终筹码净值定这局赢家。
- 多局之间用 **ELO** 维护每个模型的 rating。
- 排行榜 = 按 ELO 降序，附带：对局数、胜率、累计筹码净值。
- ⚠️ 运气噪声第一版不压（已确认「先跑通」）。后果：榜单短期不可信，多跑几局缓解；架构里 §1.2 预留复式发牌接口。

---

## 6. 分阶段实现计划

> 原则：每个里程碑结束都有**能跑、能看**的东西，不憋大招。

### 🏁 M0 · 引擎骨架 + 单模型调通（地基）

**目标：** 牌局引擎能在「全是规则 bot」的情况下打完一手牌并定胜负；同时打通「一个真实模型返回结构化动作」。

任务：
- [ ] `go mod init`，定项目目录结构（见 §7）。
- [ ] `engine`：`Card`/`Deck`（洗牌带 seed）、`HandEvaluator`（7 选 5 比大小，先跑通正确性，可借算法库）。
- [ ] `engine`：`HandState` 状态机 —— 单手牌的盲注、preflop/flop/turn/river 下注轮、摊牌。先支持 Heads-up。
- [ ] `Player` 接口 + 一个 `RuleBot`（极简策略：有牌就跟、没牌就弃）用于单测引擎。
- [ ] 单测：固定 seed 发牌，断言某手牌结果正确（牌型判定 + 筹码结算）。
- [ ] `LLMPlayer` + **第一个 provider adapter**（OpenAI 兼容），把 `Observation` 拼成 prompt，要求模型返回 §4 的结构化 JSON，解析 + 校验 + 重试。
- [ ] 命令行能跑：`1 个 LLMPlayer vs 1 个 RuleBot` 打 1 手，终端打印动作 + 内心戏。

**产出：** 终端里看到一个真实模型打一手牌，并打印出它的 reasoning。✅ 此时已验证最难的一环（模型听不听话、格式遵从）。

**风险点（M0 必须先验）：** 模型会不会老实按 JSON schema 自报 `hand_strength / is_bluffing`？小样本先测 2-3 个模型，格式遵从率太低就调 prompt / 加 few-shot。

---

### 🏁 M1 · 多模型对局 + 内心戏落库

**目标：** 多个真实模型同桌（Heads-up）打完整一局（N 手），全过程入 SQLite。

任务：
- [ ] 补齐 2-3 个 provider adapter（覆盖你手头的几家 key；非 OpenAI 兼容的单独适配）。
- [ ] `Match` 编排：发起一局，循环发手牌，维护双方筹码，到 N 手或一方破产结束。
- [ ] `Recorder` + SQLite schema：`games / hands / actions(含 self_report) / results` 四张表。
- [ ] ELO 模块：一局结束更新双方 rating。
- [ ] CLI：`pokermind run --models "gpt-x,claude-x" --hands 100`，跑完落库 + 打印 ELO 变化。
- [ ] 并发与限流：多 provider 并发调用，但加全局速率限制 + 超时 + 重试（防烧钱 / 防限速）。

**产出：** 一条命令跑完一局多模型德扑，数据库里能查到每一手、每个动作、每段内心戏。✅ **此刻价值已超过所有现存开源项目**（它们都没存内心戏）。

---

### 🏁 M2 · Web 牌桌 + 回放 + 排行榜

**目标：** 浏览器里看一场「带解说」的牌局，有逐手回放和 ELO 榜。

任务：
- [ ] Web server：`net/http` 起服务，`gorilla/websocket` 实时推牌局事件（看直播）。
- [ ] REST：`/api/games`（对局列表）、`/api/games/{id}/hands`（某局所有手牌，供回放）、`/api/leaderboard`（ELO 榜）。
- [ ] 前端单页（原生 HTML/CSS/JS，自己写）：
  - 牌桌渲染（座位、底牌、公共牌、筹码、底池）。
  - **思考气泡**：每个模型动作旁展开看它的 reasoning + 自评强度 + 诈唬意图。
  - **时间轴回放**：拖进度条逐手 / 逐动作看，能暂停看某一刻每个模型在想什么。
  - 排行榜页：ELO 排名 + 对局数 / 胜率 / 筹码净值。
- [ ] 跑一局并在前端完整回放验证。

**产出：** 一个能在浏览器演示的项目：实时看模型互打 + 事后回放看内心戏 + 排行榜。✅ 完成第一版闭环。

---

### 🏁 M3 · 扩展（按兴趣，非必须）

- [ ] 6-max 多人桌（引擎补 side pot 边池逻辑）。
- [ ] 复式发牌去运气化（§1.2 预留接口落地）。
- [ ] 决策质量评分回归：蒙特卡洛真实胜率 → 校准分 / 失误率 → 雷达图（被砍的那层）。
- [ ] 更多 provider / 本地模型（Ollama）接入。

---

## 7. 建议项目目录结构

```
pokermind/
├── go.mod
├── cmd/
│   └── pokermind/main.go        # CLI 入口（run / serve 子命令）
├── internal/
│   ├── engine/                  # 纯引擎，无外部依赖，可单测
│   │   ├── card.go              # 牌 / 牌组 / 洗牌(带 seed)
│   │   ├── evaluator.go         # 牌型比较
│   │   ├── hand.go              # 单手牌状态机
│   │   ├── hand_test.go
│   │   └── player.go            # Player 接口 + Observation/Action 定义
│   ├── players/
│   │   ├── rulebot.go           # 规则 bot（基线 + 引擎单测用）
│   │   ├── llm.go               # LLMPlayer：拼 prompt / 解析内心戏
│   │   └── providers/           # 各家 provider 的 HTTP adapter
│   │       └── openai_compat.go
│   ├── match/                   # 多手编排 + ELO
│   │   ├── match.go
│   │   └── elo.go
│   ├── store/                   # SQLite：schema + 读写 + 回放查询
│   │   └── store.go
│   └── server/                  # net/http + websocket + REST
│       └── server.go
├── web/                         # 前端单页（原创）
│   ├── index.html
│   ├── app.js
│   └── style.css
└── docs/
    └── PLAN.md                  # 本文件
```

---

## 8. 已知风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| 模型不老实自报内心戏（乱填 / 拒答 / 不守 JSON） | 内心戏数据质量差，核心价值打折 | M0 先小样本验证格式遵从；prompt 加 few-shot 示例；格式遵从率本身后续可作一个维度 |
| API 烧钱 / 限速 | 多模型 × 多手 × 每街调用，量大 | MVP 用便宜模型跑通；全局限流 + 超时 + 缓存；贵模型留到展示局 |
| Go 大模型生态弱，多 provider 适配累 | 接入成本 | 抓 OpenAI 兼容格式做主 adapter；非兼容的单独写；接入层本身当练手收益 |
| 运气噪声让榜单不可信 | 第一版排行榜参考性弱 | 已知并接受（先跑通）；M3 上复式发牌 |
| 引擎边池 / all-in 等边界 bug | 结算错误 | 第一版只做 Heads-up 规避边池；固定 seed 单测覆盖边界 |

---

## 9. 立即可执行的第一步

进入 **M0** 第一项：`go mod init` + 建目录骨架 + 先写 `engine/card.go`（牌与洗牌）并配单测。这是整个项目最确定、最可单测、零外部依赖的地基。
