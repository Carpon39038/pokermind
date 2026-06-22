# LLM Provider 通用化 + Web 现场实时对战 设计

> 日期:2026-06-22
> 范围:
>   1. LLM 接入抽象为「provider = name + kind(openai|anthropic) + base_url + api_key」,丢掉 deepseek/glm 硬编码名单。
>   2. provider 配置落 SQLite(明文),HTTP 响应里脱敏。
>   3. web 端可增删改 provider,可填表单启动对局并 SSE 实时观战(每个动作推一条事件)。
>   4. CLI `run`/`match` 改成从库里查 provider 配置;`.env` 旧变量启动时一次性迁移入库。
> 影响:`internal/players/providers`(新增 Anthropic 实现 + 工厂)、`internal/store`(新表 + 方法)、`internal/match`(新增 RunLive)、`internal/server`(新路由 + 状态)、`cmd/pokermind/main.go`(provider 工厂 + 迁移)、`web`(4 个新页面 + EventSource)。
> 不在本次:`engine` 包算法改动(只在事件层 hook)、多对局并行、对局暂停回滚介入、用户登录、加密存 apiKey。

---

## 1. 目标与范围

### 做什么
1. 抽象 LLM 接入为「provider = name + kind(openai|anthropic) + base_url + api_key」,丢掉 deepseek/glm 硬编码名单。
2. 新增 `providers.AnthropicProvider`,与 `OpenAICompatProvider` 并列。
3. provider 配置落 SQLite(明文),API key 在 HTTP 响应里脱敏。
4. web 端可增/删/改 provider,可填表单启动一局对局并 SSE 实时观战(每个动作推一条事件)。
5. CLI `run`/`match` 改成从库里查 provider 配置;`.env` 旧变量启动时一次性迁移入库。
6. 对局结束后照旧落库 + 更新 ELO,可在现有回放页重看。

### 不做什么
- 不动 `engine` 包(纯函数,不感知 LLM,只在 match 层 hook 事件)。
- 不动 `OpenAICompatProvider` 现有实现(Anthropic 是新增 sibling)。
- 不做多对局并行(同时只跑一个 live match;后续可扩)。
- 不做用户登录、不做加密存 apiKey(本地工具)。
- 不做暂停/回滚/介入对局(只有「停止」即 ctx cancel,丢弃结果)。

### 验收标准
- web 上配一个 Anthropic provider,选两个 seat 同 provider 不同 model,点开始,能在浏览器里看到逐动作出现,结束后落库,在回放页能重看。
- 旧 CLI `match --players deepseek:...,glm:...` 在 `.env` 有 key 的情况下零改动可用(自动迁移)。

---

## 2. LLM Provider 抽象

### 2.1 协议适配

`providers.Provider` 接口不变(已有 `ChatComplete(ctx, ChatRequest)(string, error)`)。新增第二个实现:

```go
// AnthropicProvider 走 Anthropic messages API。
// 端点:BaseURL + "/v1/messages"
type AnthropicProvider struct {
    BaseURL string  // 如 "https://api.anthropic.com"(不含 /v1)
    APIKey  string
    HTTP    *http.Client
}
```

Wire 协议(与 OpenAI 的差异,在适配层抹平):
- 请求:`POST {BaseURL}/v1/messages`,header `x-api-key: <key>` + `anthropic-version: 2023-06-01` + `content-type: application/json`。
- body:`{model, max_tokens, system: "...", messages: [{role:user, content:"..."}], temperature}`。注意 Anthropic 把 system 放在顶层而不是 messages 数组里。
- 响应:`{content: [{type:text, text:"..."}], ...}` —— 取第一个 text block。

Anthropic 不支持 `response_format=json_object`,现有 `LLMPlayer` 已经在 prompt 里硬约束 JSON 输出,沿用即可(实现里忽略 `ResponseFormatJSON` 字段,跟现有注释一致)。

### 2.2 工厂

`providers` 包加一个工厂,按 kind 返回具体实现:

```go
// ByKind 按 kind 字符串构造 Provider。
// kind 取值: "openai" | "anthropic"。
func ByKind(kind, baseURL, apiKey string, http *http.Client) (Provider, error)
```

`players.LLMPlayer` 不变,只是构造时传入的 `Provider` 是哪种由上层(工厂)决定。

### 2.3 测试

- `AnthropicProvider` 单测:用 `httptest.Server` mock,断言请求 header/body 正确、响应解析正确。
- `ByKind` 单测:覆盖 openai / anthropic / 未知 kind 报错三路。

---

## 3. Provider 存储 schema

### 3.1 新表

```sql
CREATE TABLE IF NOT EXISTS providers (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,        -- "deepseek", "glm", "my-anthropic", ...
    kind        TEXT NOT NULL,               -- "openai" | "anthropic"
    base_url    TEXT NOT NULL,
    api_key     TEXT NOT NULL DEFAULT '',    -- 明文
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL
);
```

### 3.2 Store API

```go
// 在 internal/store 加方法:

type ProviderCfg struct {
    ID        int64
    Name      string
    Kind      string  // "openai" | "anthropic"
    BaseURL   string
    APIKey    string  // 内部用:明文
    CreatedAt time.Time
    UpdatedAt time.Time
}

// 列表 / 按 name 查 / 增改 / 删。
// ListProviders 返回完整 api_key;HTTP 层负责脱敏。
func (s *Store) ListProviders() ([]ProviderCfg, error)
func (s *Store) GetProviderByName(name string) (*ProviderCfg, error)
func (s *Store) UpsertProvider(name, kind, baseURL, apiKey string) (*ProviderCfg, error)  // 按 name UNIQUE upsert
func (s *Store) DeleteProvider(name string) error
```

`UpsertProvider` 语义:
- `name` 不存在 → INSERT。
- `name` 存在 → UPDATE `kind`、`base_url`;`apiKey == ""` 时保留原 key(便于 web 表单只改 URL 不重输 key),`apiKey != ""` 时更新。
- 要清空 key 的场景走 DELETE + 重新 INSERT。

### 3.3 迁移

启动时(`loadDotEnv` 之后),在 `store.Open` 后跑一次 `migrateProvidersFromEnv(rec)`:

- 读 `.env` 里的 `POKERMIND_DEEPSEEK_API_KEY` → 若库 `providers` 表无 `name='deepseek'`,且 env 里 key 非空,则 INSERT `(deepseek, openai, https://api.deepseek.com, <key>)`。
- `glm` 同理,默认 base_url `https://open.bigmodel.cn/api/paas/v4`。
- 用 `INSERT OR IGNORE`,重跑幂等。

如果用户既有 `.env` 又已经在 web 上加了同名记录,以库为准(env 不覆盖)。

### 3.4 现有 schema 影响

- `players` 表已经有 `provider` 列(`RegisterPlayer(provider, model, label)`),不需要改 schema,只是 `provider` 的取值从写死的 "deepseek"/"glm" 变成库里的 provider name。旧数据兼容。
- `games`/`hands`/`actions` 完全不动。

---

## 4. 实时对局编排 `match.RunLive`

### 4.1 入口签名

```go
// LiveEvent 是 RunLive 在对局过程中向外推送的事件。
type LiveEvent struct {
    Type    string          `json:"type"`    // 见 4.3
    Payload json.RawMessage `json:"payload"` // 类型相关结构 marshal 后
}

// RunLive 与 PlayN 平级,逻辑基本一致,差别:
//   1. 每个 hook 点把 LiveEvent 非阻塞地发到 out(若已 close 则跳过)。
//   2. 返回值仍是 *ResultN,落库 + ELO 一致。
//   3. ctx 用于取消(用户在 web 上点「停止」时 cancel)。
func RunLive(
    ctx context.Context,
    specs []PlayerSpec,
    makePlayers []func() engine.Player,
    hands int,
    cfg engine.Config,
    rngSeed int64,
    rec *store.Store,
    out chan<- LiveEvent,
) (*ResultN, error)
```

`PlayN` 不动(保留给老 CLI `match` 和测试)。

### 4.2 hook 点

每个 hook 把 `LiveEvent` 发到 `out`:

| 时机 | `type` | payload |
|---|---|---|
| 对局开始(注册完玩家、知道 playerIDs) | `match_started` | `{match_id?, seats:[{seat, provider, model, label}], hands, sb, bb, starting_stack}` |
| 每手开始(发完底牌) | `hand_started` | `{hand: N, button: seat, holes:[{seat, cards}]}`(对手底牌也发到 payload,前端默认隐藏直到 showdown) |
| 每个 action 之后 | `action` | `{hand: N, seq, street, seat, provider, model, action_type, amount, self_report?}` |
| 每手结束 | `hand_finished` | `{hand: N, winners:[seat], community:[...], showdown_ranks?, pot}` |
| 对局结束 | `match_finished` | `{winner_seat, final_stacks:[...], elo_change:[...], game_id}` |

**action 事件不带 pot_before / to_call** —— 前端自己累加 pot(就是前面所有 bet/call/raise amount 的累加)。engine 包零改动。

### 4.3 LiveEvent 类型常量

```go
const (
    EvMatchStarted  = "match_started"
    EvHandStarted   = "hand_started"
    EvAction        = "action"
    EvHandFinished  = "hand_finished"
    EvMatchFinished = "match_finished"
    EvError         = "error"  // 中途出错(如 ctx cancel)
)
```

### 4.4 ctx 取消语义

- 用户点「停止」→ HTTP handler cancel ctx → `RunLive` 在下一次循环前检查 `ctx.Err()`,主动 return。
- 已发出的 `match_started`/若干 `action` 事件保留,**不落库**(丢弃)。
- 前端收不到 `match_finished` 就知道对局异常终止;服务端最后发一条 `EvError` 再关流。

### 4.5 测试

- `RunLive_test`:用 mock Player(永远 fold),跑 2 手,断言发出的 `LiveEvent` 序列满足契约。
- ctx cancel 测试:启动后立刻 cancel,断言函数在一手之内 return 且 error 含 `context.Canceled`。

---

## 5. HTTP API + SSE

### 5.1 新路由

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/api/providers` | 列出所有 provider,api_key 脱敏成 `"***1234"`(后 4 位) |
| POST | `/api/providers` | body `{name, kind, base_url, api_key?}`,upsert。api_key 为空串表示不改(但 POST 新建时必填) |
| DELETE | `/api/providers/{name}` | 删除 |
| GET | `/api/providers/{name}` | 单查(脱敏) |
| POST | `/api/matches` | 启动对局。body `{seats:[{provider, model}], hands, seed?}`。后端校验 seats 数 2-6、provider 都存在、key 非空。启动 goroutine 跑 `RunLive`。返回 `{match_id}`。 |
| GET | `/api/matches/current` | 返回当前正在跑的对局状态(若有):`{running: bool, started_at, hands_played, ...}` |
| GET | `/api/matches/current/stream` | SSE。事件类型见 §4.3。`match_finished` 后服务端关闭连接。 |
| POST | `/api/matches/current/stop` | 取消当前对局 |

### 5.2 当前 match 的内存状态(server 持有)

```go
type Server struct {
    store     *store.Store
    staticDir string
    mux       *http.ServeMux

    // 新增:当前 live 对局
    liveMu    sync.Mutex
    live      *liveMatch  // nil 表示没在跑
}

type liveMatch struct {
    id        string  // uuid 或时间戳
    cancel    context.CancelFunc
    startedAt time.Time
    subs      map[chan match.LiveEvent]struct{}  // 多个 tab 订阅同一局
}
```

设计取舍:**支持多 tab 同时观战**(每个 tab 一个 SSE 连接 = 一个 sub)。但同时只跑一个对局(POST 时若 `live != nil` 返回 409)。

事件分发:`RunLive` 写到 `server` 内部的一个 `chan match.LiveEvent`,server goroutine 读出来 fan-out 到所有 subs。sub 断开就 close 并从 map 删。缓冲足够大(比如 256),满了就丢(慢客户端是客户端的问题)。

### 5.3 SSE 帧格式

```
event: action
data: {"type":"action","payload":{"hand":1,"seq":0,"street":"preflop","seat":0,...}}

event: hand_finished
data: {...}
```

用 `event:` 字段标类型,前端 `eventSource.addEventListener("action", ...)` 分流。比混在 payload 里更标准。

### 5.4 错误情况

- 启动对局时 provider 不存在 → 400。
- 启动时已有对局在跑 → 409。
- `RunLive` 内部 error(只有 ctx cancel 这一种)→ 发 `EvError` 事件后关闭流。
- SSE 客户端断开 → 移除 sub,对局继续跑(其他 tab 还能看)。

---

## 6. 前端结构

现有前端是 3 文件 SPA:`index.html` / `app.js`(291 行,hash route) / `style.css`。继续同款纯 JS。

### 6.1 新增 hash route

| Route | 页面 |
|---|---|
| `#/` | 现有:对局列表(回放入口) |
| `#/game/{id}` | 现有:回放页 |
| `#/providers` | **新**:provider 配置页 |
| `#/live` | **新**:现场对战页(配置 + 启动) |
| `#/live/{match_id}` | **新**:观战页(SSE 流) |

### 6.2 Provider 配置页(`#/providers`)

- 表格列出所有 provider(name / kind / base_url / api_key 脱敏 / 操作)。
- 「+ 新增」表单:name(必填,唯一),kind(下拉:openai/anthropic),base_url(必填),api_key(新建必填、编辑时可选)。
- 每行「删除」按钮。
- 所有操作 fetch `/api/providers`,乐观更新。

### 6.3 现场对战页(`#/live`)

表单:
- 起手筹码、SB/BB(可选,默认 1000/5/10)
- 手数(默认 20)
- seed(默认随机)
- 座位数 N(下拉 2-6)
- N 行:每行 `<select provider>` + `<input model>`
- 「开始对局」按钮 → POST `/api/matches` → 跳 `#/live/{match_id}`

若 `GET /api/matches/current` 返回 `running:true`,页面提示「当前有对局在跑」,按钮变灰或直接跳到观战页。

### 6.4 观战页(`#/live/{match_id}`)

视图结构(沿用回放页的牌桌风格):
- 顶部:对局信息(seats、hands、进度 `第 3/20 手`)、「停止对局」按钮。
- 牌桌:6 个座位,显示 provider/model label、筹码、当前下注、底牌(自己 seat 总是可见;其他 seat 底牌在 `hand_finished` 含 showdown 时才翻)。
- 底池显示。
- 公共牌区(preflop 空 / flop 3 张 / turn 4 / river 5)。
- 事件流(右侧或底部):每个 `action` 事件追加一行 `seat0 deepseek:glm-4.6 RAISE-to-100 — "对手可能中了顶对..."(hs=0.6 eq=0.55)`。内心戏用气泡或灰字。

逻辑:
```js
const es = new EventSource(`/api/matches/current/stream`);
es.addEventListener("match_started", e => initTable(JSON.parse(e.data)));
es.addEventListener("hand_started",  e => onHandStart(JSON.parse(e.data)));
es.addEventListener("action",        e => onAction(JSON.parse(e.data)));
es.addEventListener("hand_finished", e => onHandFinish(JSON.parse(e.data)));
es.addEventListener("match_finished",e => { es.close(); showSummary(); });
```

前端从 `action.payload.amount` + `action_type` 自己累加 pot。

### 6.5 导航

顶栏加两个链接:`#/providers 配置`、`#/live 现场`。

### 6.6 不动

现有 `#/`、`#/game/{id}` 的代码完全不动。

---

## 7. CLI 兼容

### 7.1 `newLLMPlayer` 改造

```go
// 改前:switch provider 名拿 env
// 改后:从库查 provider 配置
func newLLMPlayer(rec *store.Store, providerName, model string, httpClient *http.Client) (*players.LLMPlayer, error) {
    cfg, err := rec.GetProviderByName(providerName)
    if err != nil {
        return nil, fmt.Errorf("provider %q not found in db: %w", providerName, err)
    }
    p, err := providers.ByKind(cfg.Kind, cfg.BaseURL, cfg.APIKey, httpClient)
    if err != nil {
        return nil, fmt.Errorf("provider %q kind %q: %w", providerName, cfg.Kind, err)
    }
    return &players.LLMPlayer{Provider: p, Model: model}, nil
}
```

`run`/`match` 子命令都改成先 `store.Open(*dbPath)` 再调 `newLLMPlayer(rec, ...)`。

### 7.2 `run` 子命令影响

`run` 之前不落库,现在为了查 provider 配置也需要打开 db。`run --db` flag 新增(默认 `pokermind.db`)。对用户体验的影响:跑 `run` 之前得先在 web 上配 provider(或 .env 自动迁移)。**这正是用户期望的"统一配置"**。

### 7.3 `.env` 自动迁移

启动时(`store.Open` 之后、第一次 `newLLMPlayer` 之前)调用 `migrateProvidersFromEnv(rec)`。逻辑见 §3.3。

---

## 8. 风险与未决

- **Anthropic messages API 版本**:写死 `anthropic-version: 2023-06-01`。后续 Anthropic 改版本时需要更新。短期可接受。
- **API key 泄漏面**:任何能访问 pokermind.db 或 `/api/providers` 的人都能看到明文 key。本地工具默认接受。若要部署到服务器,需要加网络层防护(不在本次范围)。
- **多 tab 观战的 sub 缓冲满了**:策略是丢事件(慢客户端问题)。若观战期间用户切后台,EventSource 会断开自动重连,但重连后历史事件不补发 —— 前端要做"重连后 fetch 一下当前 match 状态"的兜底。MVP 可不做,文档标注。
- **对局落库 vs ctx cancel**:取消的对局不落库,意味着用户看不到半截。若未来想保留"部分对局"作为教学样本,需要额外设计。本次不做。
