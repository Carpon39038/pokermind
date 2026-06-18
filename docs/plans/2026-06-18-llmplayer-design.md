# LLMPlayer + Provider Adapter 设计(M0 第五步)

> 日期:2026-06-18
> 范围:让 LLM 能作为 Player 参赛。包括 provider HTTP adapter(OpenAI 兼容格式)、LLMPlayer(prompt 拼装 + JSON 解析 + 内心戏采集)、配置加载(.env)。
> 不含:SQLite 落库、Web、Match 编排、ELO、并发限流(留 M1)。

---

## 1. 目标

跑通 PLAN §6 M0 的核心风险点:**模型能不能按 JSON schema 自报内心戏?**

具体:用 `LLMPlayer` 包一个 OpenAI 兼容的 provider,实现 `Player.Decide`。每个决策点把 `Observation` 拼成 prompt,要求模型返回结构化 JSON:
```json
{
  "reasoning": "...",
  "hand_strength": 0.0-1.0,
  "estimated_equity": 0.0-1.0,
  "is_bluffing": false,
  "action": {"type": "fold|call|raise", "amount": <raise-to>}
}
```
解析 + 校验 + 重试。返回 `Action`(把 `SelfReport` 塞进 Action 一起回 —— 这里需要给 `Action` 加 `SelfReport` 字段)。

## 2. 关键设计

### 2.1 Action 扩展(PLAN §4 的 SelfReport 落地)

现有 `Action` 只有 `Type` + `Amount`。加一个可选字段:

```go
type Action struct {
    Type       ActionType
    Amount     int
    SelfReport *SelfReport  // LLMPlayer 填;RuleBot 留 nil
}

type SelfReport struct {
    HandStrength    float64
    EstimatedEquity float64
    IsBluffing      bool
    Reasoning       string
}
```

> ⚠️ breaking change:现有 RuleBot 和测试构造的 `Action{Type: Call}` 仍兼容(SelfReport 默认 nil)。事件流 `Event.Action` 指针带上 SelfReport,落库时一并存(M1 做)。

### 2.2 Provider 抽象

```go
// Provider 是一个 LLM HTTP 后端。OpenAI 兼容格式打底。
type Provider interface {
    // ChatComplete 发一轮 chat,返回 assistant 的文本内容。
    ChatComplete(ctx context.Context, req ChatRequest) (string, error)
}

type ChatRequest struct {
    Model    string
    Messages []Message
    // ResponseFormatJSON 是否要求严格 JSON 输出(provider 不支持时忽略)
    ResponseFormatJSON bool
    Temperature        float64
    MaxTokens          int
}

type Message struct {
    Role    string // system / user / assistant
    Content string
}
```

### 2.3 OpenAI 兼容 adapter

`internal/players/providers/openai_compat.go`:

```go
type OpenAICompatProvider struct {
    BaseURL string  // 如 https://api.deepseek.com/v1 或 https://open.bigmodel.cn/api/paas/v4
    APIKey  string
    HTTP    *http.Client
}
```

- POST `{BaseURL}/chat/completions`,body 标准 OpenAI 格式。
- Header `Authorization: Bearer {APIKey}`。
- 解析 `choices[0].message.content`。
- 支持 `response_format: {type: "json_object"}`(DeepSeek/智谱均支持;不支持时退回纯 prompt 要求 JSON)。

### 2.4 LLMPlayer

`internal/players/llm.go`:

```go
type LLMPlayer struct {
    Provider Provider
    Model    string
    MaxRetries int  // JSON 解析/校验失败的重试次数,默认 2
}

func (p *LLMPlayer) Decide(obs Observation) Action {
    prompt := buildPrompt(obs)
    for attempt := 0; attempt <= p.MaxRetries; attempt++ {
        text, err := p.Provider.ChatComplete(ctx, ChatRequest{
            Model: p.Model,
            Messages: []Message{
                {Role: "system", Content: systemPrompt()},
                {Role: "user", Content: prompt},
            },
            ResponseFormatJSON: true,
            Temperature: 0.7,
        })
        if err != nil { continue }  // 网络重试
        parsed, perr := parseDecision(text)
        if perr != nil { continue }  // 格式重试
        action, verr := validateDecision(parsed, obs)
        if verr != nil { continue }  // 校验重试(如 raise-to < MinRaise)
        return action
    }
    // 重试用尽:fallback 为 fold(最保守)
    return Action{Type: Fold, SelfReport: &SelfReport{Reasoning: "LLM failed to produce valid action after retries"}}
}
```

### 2.5 prompt 设计(关键)

system prompt:
- 角色:Heads-up No-Limit Texas Hold'em 玩家
- 输出格式:严格 JSON,schema 给定
- 动作语义:fold/call/raise(raise 用 raise-to 绝对总额);ToCall=0 时 call 即 check
- 约束:amount 仅 raise 时有效,且必须 ≥ MinRaise(除非 all-in)

user prompt(每决策点):
- 当前 street、底牌、公共牌、底池
- ToCall、MinRaise、自己筹码、自己本街已投、对手本街已投
- 一段 few-shot 示例(1-2 个,降低格式不遵从率)

### 2.6 配置(.env)

`.env.example`:
```
POKERMIND_DEEPSEEK_API_KEY=
POKERMIND_DEEPSEEK_BASE_URL=https://api.deepseek.com/v1
POKERMIND_DEEPSEEK_CHAT_MODEL=deepseek-chat
POKERMIND_DEEPSEEK_REASONER_MODEL=deepseek-reasoner

POKERMIND_GLM_API_KEY=
POKERMIND_GLM_BASE_URL=https://open.bigmodel.cn/api/paas/v4
POKERMIND_GLM_MODEL=glm-4.6

POKERMIND_HTTP_TIMEOUT_SECONDS=60
```

`.gitignore` 加 `.env`(保留 `.env.example`)。

加载用 `os.Getenv`(标准库,不引 godotenv —— 让用户自己 `source .env` 或用 direnv,避免新依赖)。

## 3. 单测覆盖(不依赖网络)

- prompt 拼装:给定固定 Observation,断言 prompt 文本含关键字段(ToCall、底牌点数等)。
- JSON 解析:喂 mock 文本(合法/非法/缺字段),断言解析结果或 error。
- 决策校验:合法 raise-to / 非法 raise-to(panic?——LLMPlayer 不 panic,而是返回 fold fallback,这是与 RuleBot 的关键差别)。
- 重试:用 mock provider,前 N 次返回非法 JSON,第 N+1 次合法,断言最终成功;全失败时 fallback fold。
- 不做真实 HTTP 测试(留给 CLI 跑通时手动验证)。

## 4. 任务分解

- **Task 1:** Action 加 SelfReport 字段 + SelfReport 类型;RuleBot 与现有测试不受影响(全绿)。
- **Task 2:** Provider 接口 + OpenAICompatProvider(只 HTTP 调用,无业务);单测用 `httptest` 模拟。
- **Task 3:** LLMPlayer:buildPrompt + parseDecision + validateDecision + 重试循环;单测用 mock Provider。
- **Task 4:** .env.example + .gitignore;在 `cmd/pokermind/main.go` 加 `run` 子命令:1 LLM vs 1 RuleBot,打 1 手,打印每个动作 + reasoning。
- **Task 5:** 收尾验收(全绿 + 手动跑通说明)。

## 5. 不在本步范围

- SQLite、Web、Match、ELO。
- 并发与限流(M1)。
- 多 provider 并发跑(M1)。
- 复式发牌、决策质量评分。
