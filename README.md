# PokerMind — 多模型德州扑克擂台(看得见「内心戏」)

让多个大模型同桌打 **No-Limit Texas Hold'em**(2-6 人),把每个决策点的**内心戏**——推理、自评手牌强度、自报胜率、诈唬意图——存下来并在浏览器里逐手回放,配 ELO 排行榜。

> 与现存开源 LLM 扑克项目的差异点:它们只记录模型「做了什么」,PokerMind 还记录「**为什么这么做**」。

---

## 装

需要 Go 1.25+ 和至少一家 LLM 的 API Key(DeepSeek / 智谱 GLM)。

```bash
git clone <this-repo> pokermind
cd pokermind
go build -o pokermind ./cmd/pokermind

cp .env.example .env
# 编辑 .env,填:
#   POKERMIND_DEEPSEEK_API_KEY=...
#   POKERMIND_GLM_API_KEY=...
```

启动时自动加载同目录 `.env`;已在 shell 里 export 的同名变量优先。

> Go 模块下载若被墙:`go env -w GOPROXY=https://goproxy.cn,direct`

---

## 玩

### 1. 单手试跑(LLM vs RuleBot,验证格式遵从)

```bash
./pokermind run --provider deepseek --model deepseek-v4-flash --hands 1 --seed 1
```

终端打印每个动作 + LLM 的 reasoning。

### 2. 多模型对局(落库 + ELO)

2 人 Heads-up:

```bash
./pokermind match --players deepseek:deepseek-v4-flash,glm:glm-4.6 --hands 100 --seed 1
```

6 人桌(2-6 任意人数都行):

```bash
./pokermind match \
  --players deepseek:deepseek-v4-flash,deepseek:deepseek-v4-pro,glm:glm-4.6,glm:glm-4.6,glm:glm-4.6,glm:glm-4.6 \
  --hands 50 --seed 1
```

跑完写入 `pokermind.db`,打印每个 seat 的最终筹码、ELO 变化和排行榜。

> 旧写法 `--p1 deepseek:... --p2 glm:...` 仍兼容(仅 2 人)。

### 3. 查看排行榜

```bash
./pokermind leaderboard              # 默认读 pokermind.db
./pokermind leaderboard --db other.db
```

### 4. 启动 Web 回放 UI

```bash
./pokermind serve                    # http://localhost:8080/
./pokermind serve --addr :9090 --web ./web --db ./pokermind.db
```

浏览器里看:对局列表、逐手逐动作回放(底牌 / 公共牌 / 筹码 / 底池 / 每个动作的思考气泡)、ELO 排行榜。

---

## CLI 速查

| 子命令 | 作用 |
|---|---|
| `run` | 1 个 LLM vs RuleBot 打 N 手,终端打印动作与内心戏 |
| `match` | 2-6 个模型同桌打一整局,落 SQLite + 更新 ELO |
| `leaderboard` | 从数据库打印 ELO 排行榜 |
| `serve` | 启动 Web 回放 UI |

常用 flag:

- `run`:`--provider`、`--model`、`--hands`(默认 1)、`--seed`(默认 1)
- `match`:`--players p1,p2,...`(2-6)、`--hands`(默认 100)、`--seed`、`--db`、`--verbose`
- `serve`:`--addr`(默认 `:8080`)、`--db`、`--web`(默认 `web`)

完整 flag 见 `pokermind --help`。

### 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `POKERMIND_DEEPSEEK_API_KEY` | — | DeepSeek API Key |
| `POKERMIND_DEEPSEEK_BASE_URL` | `https://api.deepseek.com` | DeepSeek base url(不含 `/v1`) |
| `POKERMIND_GLM_API_KEY` | — | 智谱 GLM API Key |
| `POKERMIND_GLM_BASE_URL` | `https://open.bigmodel.cn/api/paas/v4` | GLM base url |
| `POKERMIND_HTTP_TIMEOUT_SECONDS` | `60` | 单次 LLM 调用超时 |

---

## 开发

```bash
go test ./...                # 引擎有固定 seed 的回归测试
go run ./cmd/pokermind --help
```

设计原则:**Engine 不感知 LLM**,只认 `Player` 接口——所以引擎可单测(喂假 Player),也能塞规则 bot 当基线。完整设计与定位见 [`docs/PLAN.md`](docs/PLAN.md)。
