# PokerMind — 多模型德州扑克擂台(看得见「内心戏」)

多个大模型同桌打 **No-Limit Texas Hold'em**(Heads-up),把每个决策点的**内心戏**——推理、自评手牌强度、自报胜率、诈唬意图——存下来并在浏览器里逐手回放,配 ELO 排行榜。

> 与现存开源 LLM 扑克项目的差异点:它们只记录模型「做了什么」,PokerMind 还记录「**为什么这么做**」。

完整设计与定位见 [`docs/PLAN.md`](docs/PLAN.md)。

---

## 功能

- **引擎**:纯 Go 写的德州扑克状态机(发牌、下注轮、摊牌、定胜负),确定性、带 seed 可复现。
- **多 provider 接入**:`Player` 接口抽象,OpenAI 兼容 adapter,目前内置 DeepSeek / GLM 两家。
- **内心戏落库**:每个动作连同 `reasoning + hand_strength + estimated_equity + is_bluffing` 一起进 SQLite。
- **多局编排 + ELO**:`match` 子命令跑一整局(默认 100 手),按筹码净值定胜负并更新双方 ELO。
- **Web 回放 UI**:`serve` 子命令起服务,浏览器里看对局列表、逐手逐动作回放(含思考气泡)、ELO 排行榜。

---

## 快速开始

### 依赖

- Go 1.25+
- 一家或两家 LLM 的 API Key(DeepSeek / 智谱 GLM)

### 安装

```bash
git clone <this-repo> pokermind
cd pokermind
go build -o pokermind ./cmd/pokermind
```

### 配置

```bash
cp .env.example .env
# 编辑 .env,填入:
#   POKERMIND_DEEPSEEK_API_KEY=...
#   POKERMIND_GLM_API_KEY=...
```

启动时会自动加载 `.env`,已在 shell 中 export 的同名变量优先。

---

## 用法

### 1. 单手试跑(LLM vs RuleBot,验证格式遵从)

```bash
./pokermind run --provider deepseek --model deepseek-v4-flash --hands 1 --seed 1
```

终端会打印每个动作 + LLM 的 reasoning。

### 2. 多模型对局(落库 + ELO)

```bash
./pokermind match \
  --p1 deepseek:deepseek-v4-flash \
  --p2 glm:glm-4.6 \
  --hands 100 \
  --seed 1
```

跑完写入 `pokermind.db`,并打印双方 ELO 变化与排行榜。`--verbose` 打开时每个 LLM 动作都打印 reasoning。

### 3. 查看排行榜

```bash
./pokermind leaderboard            # 默认读 pokermind.db
./pokermind leaderboard --db other.db
```

### 4. 启动 Web 回放 UI

```bash
./pokermind serve                  # http://localhost:8080/
./pokermind serve --addr :9090 --web ./web --db ./pokermind.db
```

浏览器打开后可看:对局列表、逐手回放(底牌 / 公共牌 / 筹码 / 底池 / 每个动作的思考气泡)、ELO 排行榜。

---

## CLI 一览

| 子命令 | 作用 |
|---|---|
| `run` | 1 个 LLM vs RuleBot 打 N 手,终端打印动作与内心戏 |
| `match` | 两个模型 Heads-up 打一整局,落 SQLite + 更新 ELO |
| `leaderboard` | 从数据库打印 ELO 排行榜 |
| `serve` | 启动 Web 回放 UI |

完整 flag 见 `pokermind --help`。

### 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `POKERMIND_DEEPSEEK_API_KEY` | — | DeepSeek API Key |
| `POKERMIND_DEEPSEEK_BASE_URL` | `https://api.deepseek.com` | DeepSeek base url |
| `POKERMIND_GLM_API_KEY` | — | 智谱 GLM API Key |
| `POKERMIND_GLM_BASE_URL` | `https://open.bigmodel.cn/api/paas/v4` | GLM base url |
| `POKERMIND_HTTP_TIMEOUT_SECONDS` | `60` | LLM 调用超时 |

---

## 项目结构

```
pokermind/
├── cmd/pokermind/main.go   # CLI 入口(run / match / leaderboard / serve)
├── internal/
│   ├── engine/             # 纯引擎:Card/Deck、HandEvaluator、HandState、Player 接口、RuleBot
│   ├── players/            # LLMPlayer + providers/(OpenAI 兼容 adapter)
│   ├── match/              # 多手编排 + ELO
│   ├── store/              # SQLite:schema + 读写 + 回放查询 + 排行榜
│   └── server/             # net/http:REST API + 静态文件
├── web/                    # 前端单页(原生 HTML/CSS/JS)
└── docs/PLAN.md            # 设计与分阶段计划
```

设计原则:**Engine 不感知 LLM**,只认 `Player` 接口——所以引擎可单测(喂假 Player),也能塞规则 bot 当基线。

---

## 当前状态

- ✅ **M0**:引擎骨架 + 单模型调通
- ✅ **M1**:多模型对局 + 内心戏落库 + ELO
- ✅ **M2**:Web 牌桌回放 + 排行榜(第一版闭环)
- 🚧 **M3**(计划中):6-max 多人桌、复式发牌去运气化、决策质量评分(蒙特卡洛真实胜率 / 校准分 / 雷达图)、Ollama 等本地模型接入

> **第一版已知取舍**:运气噪声未压(无复式发牌),榜单短期波动较大,多跑几局缓解;只存模型**自报**的胜率,**真实胜率对照**留到 M3。

---

## 开发

```bash
go test ./...                # 跑全部单测(引擎有固定 seed 的回归测试)
go run ./cmd/pokermind --help
```

Go 模块下载若被墙,设国内代理:`go env -w GOPROXY=https://goproxy.cn,direct`

---

## License

(待定)
