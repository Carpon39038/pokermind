# LLM Providers + Live Web Match Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 把 LLM 接入从写死的 deepseek/glm 抽象为「provider = name + kind(openai|anthropic) + base_url + api_key」(落 SQLite),并在 web 端支持增删改 provider、表单启动对局、SSE 实时观战每个动作。

**Architecture:** 新增 `providers.AnthropicProvider` 与 `OpenAICompatProvider` 并列;`store` 加 `providers` 表;`match.RunLive` 与 `PlayN` 平级、把 5 类事件推到 `chan<- LiveEvent`;`server` 加 `/api/providers`、`/api/matches`、`/api/matches/current/stream`(SSE);前端 4 个新页面(providers / live 配置 / live 观战)。CLI `newLLMPlayer` 改读库,`.env` 启动时一次性迁移。

**Tech Stack:** Go 1.25 stdlib + `modernc.org/sqlite`;原生 HTML/CSS/JS(无构建);SSE(EventSource)。

**Design doc:** [`docs/plans/2026-06-22-llm-providers-and-live-design.md`](2026-06-22-llm-providers-and-live-design.md)

---

## Task 序列与依赖

- T1–T4 是独立的后端基础(provider 抽象 / store / match / server)。
- T5(server live 状态 + SSE)依赖 T3、T4。
- T6(CLI 改造 + .env 迁移)依赖 T2、T4。
- T7(前端 providers 页)依赖 T4。
- T8(前端 live 配置页 + 观战页)依赖 T5、T7。
- T9 端到端验证最后跑。

每 task 末尾 commit。每 task 内部 TDD(先写测试再实现)。

---

## Task 1: AnthropicProvider 实现

**Files:**
- Create: `internal/players/providers/anthropic.go`
- Create: `internal/players/providers/anthropic_test.go`

**Step 1: Write the failing test**

`internal/players/providers/anthropic_test.go`:

```go
package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicProvider_ChatComplete_request_and_parse(t *testing.T) {
	var gotHeaders http.Header
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &gotBody)

		// Anthropic messages API 响应格式
		resp := map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "hello world"},
			},
		}
		b, _ := json.Marshal(resp)
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	p := &AnthropicProvider{
		BaseURL: strings.TrimSuffix(srv.URL, "/v1"),
		APIKey:  "sk-test",
	}
	out, err := p.ChatComplete(context.Background(), ChatRequest{
		Model:       "claude-3-5-sonnet-20241022",
		Temperature: 0.7,
		MaxTokens:   100,
		Messages: []Message{
			{Role: "system", Content: "be good"},
			{Role: "user", Content: "hi"},
		},
		ResponseFormatJSON: true, // Anthropic 应忽略此字段
	})
	if err != nil {
		t.Fatalf("ChatComplete err: %v", err)
	}
	if out != "hello world" {
		t.Errorf("got %q want %q", out, "hello world")
	}

	// header 校验
	if gotHeaders.Get("x-api-key") != "sk-test" {
		t.Errorf("x-api-key header missing/wrong: %q", gotHeaders.Get("x-api-key"))
	}
	if gotHeaders.Get("anthropic-version") == "" {
		t.Errorf("anthropic-version header missing")
	}
	// 路径
	if !strings.HasSuffix(gotHeaders.Get(""), "") {
		// r.URL.Path 在 server 侧查更清楚
	}
	// body 校验:system 应在顶层,不在 messages 里
	if sys, _ := gotBody["system"].(string); sys != "be good" {
		t.Errorf("system field = %v want %q", gotBody["system"], "be good")
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 1 {
		t.Errorf("messages len = %d want 1 (system 应拆出)", len(msgs))
	}
}

func TestAnthropicProvider_missing_baseurl_or_key(t *testing.T) {
	p := &AnthropicProvider{}
	if _, err := p.ChatComplete(context.Background(), ChatRequest{}); err == nil {
		t.Error("expected error for empty BaseURL")
	}
	p.BaseURL = "https://x"
	if _, err := p.ChatComplete(context.Background(), ChatRequest{}); err == nil {
		t.Error("expected error for empty APIKey")
	}
}

func TestAnthropicProvider_HTTP_non_2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad"}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	p := &AnthropicProvider{BaseURL: srv.URL, APIKey: "k"}
	_, err := p.ChatComplete(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("want HTTP 400 err, got %v", err)
	}
}
```

补一条路径校验(更干净),把上面 `gotHeaders` 那段无效断言换成:

```go
// 改写 srv handler 多记一个 path:
var gotPath string
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    gotPath = r.URL.Path
    ...
}))
...
// 末尾:
if !strings.HasSuffix(gotPath, "/v1/messages") {
    t.Errorf("path = %q want suffix /v1/messages", gotPath)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/players/providers/ -run Anthropic -v`
Expected: FAIL `undefined: AnthropicProvider`。

**Step 3: Write minimal implementation**

`internal/players/providers/anthropic.go`:

```go
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// AnthropicProvider 走 Anthropic messages API。
// 端点:BaseURL + "/v1/messages"。system 放在顶层而非 messages。
type AnthropicProvider struct {
	BaseURL string // 不含末尾 /,如 "https://api.anthropic.com"
	APIKey  string
	HTTP    *http.Client
}

// anthropicRequest 是 Anthropic messages API 的请求体。
type anthropicRequest struct {
	Model       string             `json:"model"`
	Messages    []anthropicMessage `json:"messages"`
	System      string             `json:"system,omitempty"`
	Temperature float64            `json:"temperature,omitempty"`
	MaxTokens   int                `json:"max_tokens,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicResponse 是 Anthropic messages API 的响应体(只解析需要的字段)。
type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// ChatComplete 实现 Provider。
func (p *AnthropicProvider) ChatComplete(ctx context.Context, req ChatRequest) (string, error) {
	if p.BaseURL == "" {
		return "", fmt.Errorf("AnthropicProvider: BaseURL empty")
	}
	if p.APIKey == "" {
		return "", fmt.Errorf("AnthropicProvider: APIKey empty")
	}

	// 把 system message 拆出来(Anthropic 不放 messages 数组)
	var systemText string
	body := anthropicRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	for _, m := range req.Messages {
		if m.Role == "system" {
			if systemText != "" {
				systemText += "\n"
			}
			systemText += m.Content
			continue
		}
		body.Messages = append(body.Messages, anthropicMessage{Role: m.Role, Content: m.Content})
	}
	body.System = systemText

	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	client := p.HTTP
	if client == nil {
		client = DefaultHTTPClient(0)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		return "", fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, buf.String())
	}

	var parsed anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	for _, block := range parsed.Content {
		if block.Type == "text" && block.Text != "" {
			return block.Text, nil
		}
	}
	return "", fmt.Errorf("anthropic returned no text content")
}

// 编译期保证接口实现
var _ Provider = (*AnthropicProvider)(nil)

// 引用 strings 防止 import 未用(测试文件用,这里在 init 暂无;留作 future)
var _ = strings.TrimSpace
```

> 若 `strings` 在实现里没用到,删掉那行 `var _ = strings.TrimSpace` 和对应 import —— 测试文件里用 strings 不影响实现文件。

**Step 4: Run test to verify it passes**

Run: `go test ./internal/players/providers/ -run Anthropic -v`
Expected: PASS。

**Step 5: Commit**

```bash
git add internal/players/providers/anthropic.go internal/players/providers/anthropic_test.go
git commit -m "feat(providers): AnthropicProvider 走 messages API(Task 1)"
```

---

## Task 2: providers.ByKind 工厂

**Files:**
- Modify: `internal/players/providers/provider.go`
- Create: `internal/players/providers/factory_test.go`

**Step 1: Write the failing test**

`internal/players/providers/factory_test.go`:

```go
package providers

import "testing"

func TestByKind(t *testing.T) {
	cases := []struct {
		kind    string
		wantErr bool
		wantTyp string
	}{
		{"openai", false, "*providers.OpenAICompatProvider"},
		{"anthropic", false, "*providers.AnthropicProvider"},
		{"", true, ""},
		{"unknown", true, ""},
		{"OPENAI", false, "*providers.OpenAICompatProvider"}, // 大小写容错
	}
	for _, c := range cases {
		p, err := ByKind(c.kind, "https://x", "k", nil)
		if c.wantErr {
			if err == nil {
				t.Errorf("kind %q: want err got nil (%T)", c.kind, p)
			}
			continue
		}
		if err != nil {
			t.Errorf("kind %q: unexpected err %v", c.kind, err)
			continue
		}
		typ := fmt.Sprintf("%T", p)
		if typ != c.wantTyp {
			t.Errorf("kind %q: type = %s want %s", c.kind, typ, c.wantTyp)
		}
	}
}
```

(需要 `import "fmt"`)

**Step 2: Run test to verify it fails**

Run: `go test ./internal/players/providers/ -run ByKind -v`
Expected: FAIL `undefined: ByKind`。

**Step 3: Write minimal implementation**

在 `internal/players/providers/provider.go` 末尾追加:

```go
import "strings"  // 若未导入则补到 import 块

// ByKind 按 kind 字符串构造 Provider。
// kind 取值(大小写不敏感):"openai" | "anthropic"。
func ByKind(kind, baseURL, apiKey string, http *http.Client) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "openai":
		return &OpenAICompatProvider{BaseURL: baseURL, APIKey: apiKey, HTTP: http}, nil
	case "anthropic":
		return &AnthropicProvider{BaseURL: baseURL, APIKey: apiKey, HTTP: http}, nil
	default:
		return nil, fmt.Errorf("ByKind: unknown kind %q (want openai or anthropic)", kind)
	}
}
```

(若 provider.go 还没 import `fmt` 和 `strings`,一并补上。)

**Step 4: Run test to verify it passes**

Run: `go test ./internal/players/providers/ -v`
Expected: 所有测试 PASS。

**Step 5: Commit**

```bash
git add internal/players/providers/provider.go internal/players/providers/factory_test.go
git commit -m "feat(providers): ByKind 工厂按 kind 返回 Provider(Task 2)"
```

---

## Task 3: store providers 表 + CRUD

**Files:**
- Modify: `internal/store/store.go`(schema migration 加 providers 表;新方法)
- Modify: `internal/store/store_test.go`(或新建 `internal/store/providers_test.go`)

**Step 1: Write the failing test**

`internal/store/providers_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
)

func TestProvidersCRUD(t *testing.T) {
	rec := newTestStore(t)
	defer rec.Close()

	// List 空
	got, err := rec.ListProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("initial list = %d want 0", len(got))
	}

	// Upsert 新增
	p, err := rec.UpsertProvider("deepseek", "openai", "https://api.deepseek.com", "sk-abc")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "deepseek" || p.Kind != "openai" || p.APIKey != "sk-abc" {
		t.Errorf("upsert returned wrong: %+v", p)
	}

	// GetByName
	g, err := rec.GetProviderByName("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if g.APIKey != "sk-abc" {
		t.Errorf("getbyname apikey = %q", g.APIKey)
	}

	// Upsert 已存在:空 apiKey 不覆盖
	_, err = rec.UpsertProvider("deepseek", "openai", "https://api.deepseek.com/v2", "")
	if err != nil {
		t.Fatal(err)
	}
	g, _ = rec.GetProviderByName("deepseek")
	if g.APIKey != "sk-abc" {
		t.Errorf("empty apiKey should not overwrite; got %q", g.APIKey)
	}
	if g.BaseURL != "https://api.deepseek.com/v2" {
		t.Errorf("base_url not updated; got %q", g.BaseURL)
	}

	// Upsert 已存在:非空 apiKey 覆盖
	_, _ = rec.UpsertProvider("deepseek", "openai", "https://api.deepseek.com/v2", "sk-new")
	g, _ = rec.GetProviderByName("deepseek")
	if g.APIKey != "sk-new" {
		t.Errorf("apiKey should be overwritten; got %q", g.APIKey)
	}

	// List 含一条
	got, _ = rec.ListProviders()
	if len(got) != 1 {
		t.Errorf("list len = %d want 1", len(got))
	}

	// Delete
	if err := rec.DeleteProvider("deepseek"); err != nil {
		t.Fatal(err)
	}
	got, _ = rec.ListProviders()
	if len(got) != 0 {
		t.Errorf("after delete list = %d want 0", len(got))
	}

	// Delete 不存在的应报错或 no-op(选报错)
	if err := rec.DeleteProvider("nonexistent"); err == nil {
		t.Error("delete nonexistent should error")
	}
}

// newTestStore 开一个临时 SQLite,辅助测试。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	rec, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	return rec
}
```

> 若 `newTestStore` 已在别的 test 文件里定义,直接复用,不要重复定义。

**Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run Providers -v`
Expected: FAIL `undefined: ListProviders`。

**Step 3: Write minimal implementation**

在 `internal/store/store.go`:

1. 在 `migrate()` 的 `schema` 字符串里(在 `players` 表后、`games` 前,或末尾均可)加:

```sql
CREATE TABLE IF NOT EXISTS providers (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    kind        TEXT NOT NULL,
    base_url    TEXT NOT NULL,
    api_key     TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
```

2. 文件末尾追加方法:

```go
// ProviderCfg 是 providers 表的一行。
type ProviderCfg struct {
	ID        int64
	Name      string
	Kind      string
	BaseURL   string
	APIKey    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ListProviders 返回所有 provider,按 name 升序。
func (s *Store) ListProviders() ([]ProviderCfg, error) {
	rows, err := s.db.Query(`
		SELECT id, name, kind, base_url, api_key, created_at, updated_at
		FROM providers
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()
	var out []ProviderCfg
	for rows.Next() {
		var p ProviderCfg
		var created, updated string
		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKey, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan provider: %w", err)
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339, created)
		p.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProviderByName 按 name 精确查。不存在返回 (nil, nil)。
func (s *Store) GetProviderByName(name string) (*ProviderCfg, error) {
	var p ProviderCfg
	var created, updated string
	err := s.db.QueryRow(`
		SELECT id, name, kind, base_url, api_key, created_at, updated_at
		FROM providers WHERE name = ?
	`, name).Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKey, &created, &updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get provider %q: %w", name, err)
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339, created)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return &p, nil
}

// UpsertProvider 按 name UNIQUE upsert。
//   - name 不存在 → INSERT。
//   - name 存在   → UPDATE kind, base_url;apiKey == "" 时保留原 key,否则覆盖。
func (s *Store) UpsertProvider(name, kind, baseURL, apiKey string) (*ProviderCfg, error) {
	if name == "" {
		return nil, fmt.Errorf("UpsertProvider: name empty")
	}
	if kind == "" {
		return nil, fmt.Errorf("UpsertProvider: kind empty")
	}
	if baseURL == "" {
		return nil, fmt.Errorf("UpsertProvider: baseURL empty")
	}
	now := time.Now().Format(time.RFC3339)

	// 先查是否存在
	existing, err := s.GetProviderByName(name)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		if apiKey == "" {
			return nil, fmt.Errorf("UpsertProvider: apiKey required for new provider %q", name)
		}
		_, err := s.db.Exec(
			`INSERT INTO providers(name, kind, base_url, api_key, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			name, kind, baseURL, apiKey, now, now,
		)
		if err != nil {
			return nil, fmt.Errorf("insert provider: %w", err)
		}
	} else {
		newKey := apiKey
		if newKey == "" {
			newKey = existing.APIKey
		}
		_, err := s.db.Exec(
			`UPDATE providers SET kind=?, base_url=?, api_key=?, updated_at=? WHERE id=?`,
			kind, baseURL, newKey, now, existing.ID,
		)
		if err != nil {
			return nil, fmt.Errorf("update provider: %w", err)
		}
	}
	return s.GetProviderByName(name)
}

// DeleteProvider 删除一个 provider。不存在返回 error。
func (s *Store) DeleteProvider(name string) error {
	res, err := s.db.Exec(`DELETE FROM providers WHERE name=?`, name)
	if err != nil {
		return fmt.Errorf("delete provider: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("delete provider: %q not found", name)
	}
	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run Providers -v`
Expected: PASS。再跑 `go test ./internal/store/` 确保 schema 改动没破坏其他测试。

**Step 5: Commit**

```bash
git add internal/store/store.go internal/store/providers_test.go
git commit -m "feat(store): providers 表 + CRUD(Task 3)"
```

---

## Task 4: match.RunLive

**Files:**
- Modify: `internal/match/match.go`(在 PlayN 后追加 RunLive 与 LiveEvent)
- Modify: `internal/match/match_test.go`(或新建 `internal/match/live_test.go`)

**Step 1: Write the failing test**

`internal/match/live_test.go`:

```go
package match

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"pokermind/internal/engine"
)

// alwaysFoldPlayer 永远 fold 的 mock Player。
type alwaysFoldPlayer struct{}

func (alwaysFoldPlayer) Decide(obs engine.Observation) engine.Action {
	return engine.Action{Type: engine.Fold}
}

func TestRunLive_EventSequence(t *testing.T) {
	specs := []PlayerSpec{
		{Provider: "p", Model: "A", Label: "A"},
		{Provider: "p", Model: "B", Label: "B"},
	}
	makePlayers := []func() engine.Player{
		func() engine.Player { return alwaysFoldPlayer{} },
		func() engine.Player { return alwaysFoldPlayer{} },
	}
	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out := make(chan LiveEvent, 256)

	done := make(chan error, 1)
	go func() {
		_, err := RunLive(ctx, specs, makePlayers, 2, cfg, 42, nil, out)
		done <- err
		close(out)
	}()

	var types []string
	for ev := range out {
		types = append(types, ev.Type)
	}

	err := <-done
	if err != nil {
		t.Fatalf("RunLive err: %v", err)
	}

	// 期望:match_started, (hand_started, action*1~2, hand_finished) × 2, match_finished
	if len(types) < 4 {
		t.Fatalf("too few events: %v", types)
	}
	if types[0] != EvMatchStarted {
		t.Errorf("first event = %q want %q", types[0], EvMatchStarted)
	}
	if types[len(types)-1] != EvMatchFinished {
		t.Errorf("last event = %q want %q", types[len(types)-1], EvMatchFinished)
	}

	// 校验 payload 可正确 unmarshal
	// (在循环里多收一些 typed payload 校验)
}

func TestRunLive_CancelContext(t *testing.T) {
	specs := []PlayerSpec{
		{Provider: "p", Model: "A", Label: "A"},
		{Provider: "p", Model: "B", Label: "B"},
	}
	// 慢 Player:阻塞一会,保证 cancel 落在循环中间
	slow := slowPlayer{d: 2 * time.Second}
	makePlayers := []func() engine.Player{
		func() engine.Player { return slow },
		func() engine.Player { return alwaysFoldPlayer{} },
	}
	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan LiveEvent, 256)

	done := make(chan error, 1)
	go func() {
		_, err := RunLive(ctx, specs, makePlayers, 10, cfg, 1, nil, out)
		done <- err
		close(out)
	}()

	cancel() // 立刻取消
	select {
	case err := <-done:
		if err == nil {
			t.Error("want context.Canceled error, got nil")
		} else if !errors.Is(err, context.Canceled) {
			t.Errorf("want context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunLive did not return after cancel within 5s")
	}
}

type slowPlayer struct{ d time.Duration }

func (s slowPlayer) Decide(obs engine.Observation) engine.Action {
	time.Sleep(s.d)
	return engine.Action{Type: engine.Fold}
}

// _ 引用,防止 import 未用
var _ = json.Unmarshal
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/match/ -run RunLive -v`
Expected: FAIL `undefined: RunLive, LiveEvent, EvMatchStarted, ...`。

**Step 3: Write minimal implementation**

在 `internal/match/match.go` 末尾追加:

```go
import (
	"context"
	"encoding/json"
	// ... 保留原有 imports
)

// LiveEvent 是 RunLive 在对局过程中向外推送的事件。
type LiveEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// LiveEvent 类型常量。
const (
	EvMatchStarted  = "match_started"
	EvHandStarted   = "hand_started"
	EvAction        = "action"
	EvHandFinished  = "hand_finished"
	EvMatchFinished = "match_finished"
	EvError         = "error"
)

// matchStartedPayload 见 EvMatchStarted。
type matchStartedPayload struct {
	Seats []struct {
		Seat     int    `json:"seat"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Label    string `json:"label"`
	} `json:"seats"`
	Hands          int `json:"hands"`
	SmallBlind     int `json:"sb"`
	BigBlind       int `json:"bb"`
	StartingStack  int `json:"starting_stack"`
}

type handStartedPayload struct {
	Hand   int `json:"hand"`
	Button int `json:"button"`
	Holes  []struct {
		Seat  int      `json:"seat"`
		Cards []string `json:"cards"`
	} `json:"holes"`
}

type actionPayload struct {
	Hand       int     `json:"hand"`
	Seq        int     `json:"seq"`
	Street     string  `json:"street"`
	Seat       int     `json:"seat"`
	Provider   string  `json:"provider"`
	Model      string  `json:"model"`
	ActionType string  `json:"action_type"`
	Amount     int     `json:"amount"`
	HasReport  bool    `json:"has_report,omitempty"`
	Reasoning  string  `json:"reasoning,omitempty"`
	HandStr    float64 `json:"hand_strength,omitempty"`
	EstEquity  float64 `json:"estimated_equity,omitempty"`
	IsBluffing bool    `json:"is_bluffing,omitempty"`
}

type handFinishedPayload struct {
	Hand     int      `json:"hand"`
	Winners  []int    `json:"winners"`
	Community []string `json:"community"`
	Pot      int      `json:"pot"`
	Folded   bool     `json:"folded"`
}

type matchFinishedPayload struct {
	WinnerSeat  int       `json:"winner_seat"`
	FinalStacks []int     `json:"final_stacks"`
	EloChange   []float64 `json:"elo_change,omitempty"`
	GameID      int64     `json:"game_id,omitempty"`
}

// RunLive 与 PlayN 平级,差别:
//  1. 每个事件 hook 把 LiveEvent 发到 out(非阻塞,满则丢)。
//  2. ctx 用于取消(用户点「停止」),取消时立刻 return error,不落库。
//  3. 落库 + ELO 逻辑同 PlayN。
func RunLive(
	ctx context.Context,
	specs []PlayerSpec,
	makePlayers []func() engine.Player,
	hands int,
	cfg engine.Config,
	rngSeed int64,
	rec *store.Store,
	out chan<- LiveEvent,
) (*ResultN, error) {
	n := len(specs)
	if n < 2 || n > 6 {
		return nil, fmt.Errorf("RunLive: need 2-6 specs, got %d", n)
	}
	if len(makePlayers) != n {
		return nil, fmt.Errorf("RunLive: makePlayers length %d != specs length %d", len(makePlayers), n)
	}
	if hands <= 0 {
		return nil, fmt.Errorf("RunLive: hands must be > 0")
	}
	if cfg.BigBlind <= 0 || cfg.SmallBlind <= 0 {
		return nil, fmt.Errorf("RunLive: invalid blinds")
	}

	// 注册玩家
	var playerIDs []int64
	if rec != nil {
		playerIDs = make([]int64, n)
		for i, spec := range specs {
			id, err := rec.RegisterPlayer(spec.Provider, spec.Model, spec.Label)
			if err != nil {
				return nil, fmt.Errorf("register player %d: %w", i, err)
			}
			playerIDs[i] = id
		}
	} else {
		playerIDs = make([]int64, n)
	}

	// 推 match_started
	var msPayload matchStartedPayload
	msPayload.Hands = hands
	msPayload.SmallBlind = cfg.SmallBlind
	msPayload.BigBlind = cfg.BigBlind
	msPayload.StartingStack = cfg.StartingStack
	for i, spec := range specs {
		msPayload.Seats = append(msPayload.Seats, struct {
			Seat     int    `json:"seat"`
			Provider string `json:"provider"`
			Model    string `json:"model"`
			Label    string `json:"label"`
		}{Seat: i, Provider: spec.Provider, Model: spec.Model, Label: spec.Label})
	}
	emitLive(out, LiveEvent{Type: EvMatchStarted, mustPayload(msPayload)})

	stacks := make([]int, n)
	for i := range stacks {
		stacks[i] = cfg.StartingStack
	}
	rng := rand.New(rand.NewSource(rngSeed))
	startedAt := time.Now()

	gameRecord := store.GameRecord{
		NumSeats:   n,
		Seats:      make([]store.GameSeat, n),
		StartedAt:  startedAt,
		ConfigJSON: configJSON(cfg, hands),
	}

	handsPlayed := 0
	for h := 1; h <= hands; h++ {
		// ctx cancel 检查
		if err := ctx.Err(); err != nil {
			emitLive(out, LiveEvent{Type: EvError, mustPayload(map[string]string{"error": err.Error()})})
			return nil, err
		}

		bust := false
		for _, s := range stacks {
			if s < cfg.BigBlind {
				bust = true
				break
			}
		}
		if bust {
			break
		}

		button := (h - 1) % n
		seats := make([]engine.PlayerSeat, n)
		for i := 0; i < n; i++ {
			seats[i] = engine.PlayerSeat{
				ID:     i,
				Stack:  stacks[i],
				Player: makePlayers[i](),
			}
		}
		events, result := engine.PlayHand(seats, button, cfg, rng, h)
		stacks = result.FinalStacks

		// 从 events 抽 hand_started / action / hand_finished 推送
		emitHandEvents(out, events, h, button, specs, result)

		if rec != nil {
			hr := translateHand(h, button, events, result, playerIDs)
			gameRecord.Hands = append(gameRecord.Hands, hr)
		}
		handsPlayed++
	}

	// 最终 ctx 检查(防止刚跑完最后一手被 cancel)
	if err := ctx.Err(); err != nil {
		emitLive(out, LiveEvent{Type: EvError, mustPayload(map[string]string{"error": err.Error()})})
		return nil, err
	}

	winnerSeat := -1
	best := -1
	for i, s := range stacks {
		if s > best {
			best = s
			winnerSeat = i
		}
	}

	out2 := &ResultN{
		HandsPlayed: handsPlayed,
		WinnerSeat:  winnerSeat,
		FinalStacks: stacks,
	}

	if rec != nil {
		isDraw := (winnerSeat == -1)
		for i, finalChips := range stacks {
			gameRecord.Seats[i] = store.GameSeat{
				PlayerID:   playerIDs[i],
				FinalChips: finalChips,
				IsWinner:   !isDraw && i == winnerSeat,
			}
		}
		gameRecord.HandsPlayed = handsPlayed
		gameRecord.IsDraw = isDraw
		gameRecord.FinishedAt = time.Now()

		gameID, err := rec.RecordGame(gameRecord)
		if err != nil {
			return nil, fmt.Errorf("record game: %w", err)
		}
		out2.GameID = gameID
		out2.PlayerIDs = playerIDs
		out2.EloChange = make([]float64, n)

		if !isDraw && winnerSeat >= 0 {
			elos := make([]float64, n)
			for i, pid := range playerIDs {
				e, _ := rec.GetElo(pid)
				elos[i] = float64(e)
			}
			winnerRating := elos[winnerSeat]
			var loserRatings []float64
			for i, e := range elos {
				if i != winnerSeat {
					loserRatings = append(loserRatings, e)
				}
			}
			newWinner, newLosers := elo.UpdateMulti(winnerRating, loserRatings, 0)
			loserIdx := 0
			for i, pid := range playerIDs {
				if i == winnerSeat {
					_ = rec.SetElo(pid, int(newWinner))
					out2.EloChange[i] = newWinner - elos[i]
				} else {
					_ = rec.SetElo(pid, int(newLosers[loserIdx]))
					out2.EloChange[i] = newLosers[loserIdx] - elos[i]
					loserIdx++
				}
			}
		}
	}

	// 推 match_finished
	mfPayload := matchFinishedPayload{
		WinnerSeat:  winnerSeat,
		FinalStacks: stacks,
		EloChange:   out2.EloChange,
		GameID:      out2.GameID,
	}
	emitLive(out, LiveEvent{Type: EvMatchFinished, mustPayload(mfPayload)})

	return out2, nil
}

// emitHandEvents 从 engine events 抽出 hand_started / action / hand_finished 推送。
// 规则:
//   - 第一次 DealtHole(以及后续 DealtHole)累积底牌 → hand_started(只在第一手或每手第一个 DealtHole 触发?engine 每个 seat 一个 DealtHole,我们一次性收集再发)。
//   - ActionTaken → action 事件。
//   - HandFinished → hand_finished 事件。
//
// 简化策略:遍历一次,分 3 阶段(底牌收集 / 动作流 / 结束)。
func emitHandEvents(out chan<- LiveEvent, events []engine.Event, hand int, button int, specs []PlayerSpec, result engine.HandResult) {
	var hs handStartedPayload
	hs.Hand = hand
	hs.Button = button
	handStartedSent := false

	sendHandStarted := func() {
		if handStartedSent {
			return
		}
		handStartedSent = true
		emitLive(out, LiveEvent{Type: EvHandStarted, mustPayload(hs)})
	}

	for _, ev := range events {
		switch ev.Type {
		case engine.DealtHole:
			if ev.Seat >= 0 && ev.Seat < len(specs) {
				cards := make([]string, 0, len(ev.Cards))
				for _, c := range ev.Cards {
					cards = append(cards, c.String())
				}
				hs.Holes = append(hs.Holes, struct {
					Seat  int      `json:"seat"`
					Cards []string `json:"cards"`
				}{Seat: ev.Seat, Cards: cards})
			}
			// 收完所有 seat 底牌才发?engine 里 DealtHole 一批连续出现。
			// 简化:第一个 ActionTaken 或 HandFinished 之前若不再有 DealtHole 才发 ——
			// 这里用一个简单启发:DealtHole 出现就发(若有多个,合并到一条)。
			// 实际 engine 一次性发出 N 个 DealtHole,这条会触发 N 次 sendHandStarted,
			// 但 handStartedSent 只发一次,底牌累积到 hs.Holes。
			sendHandStarted()
		case engine.ActionTaken:
			sendHandStarted() // 防御:万一 DealtHole 未触发
			if ev.Action == nil {
				continue
			}
			ap := actionPayload{
				Hand:       hand,
				Street:     ev.Street.String(),
				Seat:       ev.Seat,
				Provider:   specs[ev.Seat].Provider,
				Model:      specs[ev.Seat].Model,
				ActionType: ev.Action.Type.String(),
				Amount:     ev.Action.Amount,
			}
			if ev.Action.SelfReport != nil {
				ap.HasReport = true
				ap.Reasoning = ev.Action.SelfReport.Reasoning
				ap.HandStr = ev.Action.SelfReport.HandStrength
				ap.EstEquity = ev.Action.SelfReport.EstimatedEquity
				ap.IsBluffing = ev.Action.SelfReport.IsBluffing
			}
			emitLive(out, LiveEvent{Type: EvAction, mustPayload(ap)})
		case engine.HandFinished:
			sendHandStarted()
			community := []string{}
			for _, ev2 := range events {
				if ev2.Type == engine.StreetAdvanced {
					for _, c := range ev2.Cards {
						community = append(community, c.String())
					}
				}
			}
			hf := handFinishedPayload{
				Hand:      hand,
				Winners:   ev.Winners,
				Community: community,
				Pot:       result.PotWon,
				Folded:    ev.Folded,
			}
			emitLive(out, LiveEvent{Type: EvHandFinished, mustPayload(hf)})
		}
	}
}

// emitLive 非阻塞地把 ev 发到 out;满则丢。
func emitLive(out chan<- LiveEvent, ev LiveEvent) {
	if out == nil {
		return
	}
	select {
	case out <- ev:
	default:
		// 缓冲满,丢事件(慢客户端问题)
	}
}

// mustPayload 把 v marshal 成 RawMessage,失败时用 error payload。
func mustPayload(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(map[string]string{"_marshal_error": err.Error()})
	}
	return b
}
```

> 上面 `elo` 引用要确认包名:`internal/elo` 的 `UpdateMulti` 在 PlayN 里已用过,直接复用。

**Step 4: Run test to verify it passes**

Run: `go test ./internal/match/ -v`
Expected: PASS(包括现有 PlayN 测试)。

**Step 5: Commit**

```bash
git add internal/match/match.go internal/match/live_test.go
git commit -m "feat(match): RunLive + 5 类 LiveEvent 推送(Task 4)"
```

---

## Task 5: server providers API + live state + SSE

这是最大一个 task。拆成三个子 commit。

### 5a. `/api/providers` 路由

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/server_test.go`

**Step 1: Write the failing test**

在 `internal/server/server_test.go` 追加:

```go
func TestProvidersAPI_CRUD(t *testing.T) {
	rec := newTestStore(t) // 若已存在则复用;否则复制 Task 3 的 newTestStore(注意 server_test 包名是 server,需跨包 helper 或在 server 包内再写一个)
	defer rec.Close()
	srv := New(rec, "")

	// List 空
	req := httptest.NewRequest("GET", "/api/providers", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("list status = %d", w.Code)
	}
	var list []map[string]any
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 0 {
		t.Errorf("list len = %d", len(list))
	}

	// Create
	body := strings.NewReader(`{"name":"deepseek","kind":"openai","base_url":"https://api.deepseek.com","api_key":"sk-xxx1234"}`)
	req = httptest.NewRequest("POST", "/api/providers", body)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("create status = %d body=%s", w.Code, w.Body.String())
	}

	// List 含一条,apiKey 脱敏
	req = httptest.NewRequest("GET", "/api/providers", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Fatalf("list len = %d", len(list))
	}
	key, _ := list[0]["api_key"].(string)
	if key == "sk-xxx1234" || !strings.HasSuffix(key, "1234") || !strings.HasPrefix(key, "***") {
		t.Errorf("api_key not masked: %q", key)
	}

	// Delete
	req = httptest.NewRequest("DELETE", "/api/providers/deepseek", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("delete status = %d", w.Code)
	}
}
```

> 需要 import:`encoding/json`、`net/http/httptest`、`strings`。

**Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run ProvidersAPI -v`
Expected: FAIL(路由不存在,404 或类似)。

**Step 3: Write minimal implementation**

在 `internal/server/server.go`:

1. 在 `routes()` 里加:

```go
s.mux.HandleFunc("/api/providers", s.handleProviders)        // GET list, POST upsert
s.mux.HandleFunc("/api/providers/", s.handleProviderByName)  // GET / DELETE {name}
```

2. 在文件末尾追加 handler + maskKey helper:

```go
// maskedKey 把 apiKey 脱敏成 "***1234"(后 4 位)。
func maskedKey(key string) string {
	if len(key) <= 4 {
		return "***"
	}
	return "***" + key[len(key)-4:]
}

// providerJSON 是 HTTP 返回的 provider 结构(apiKey 脱敏)。
type providerJSON struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"` // 脱敏
}

func toProviderJSON(p store.ProviderCfg) providerJSON {
	return providerJSON{ID: p.ID, Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL, APIKey: maskedKey(p.APIKey)}
}

// handleProviders: GET 列表(脱敏),POST upsert。
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.store.ListProviders()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		out := make([]providerJSON, 0, len(list))
		for _, p := range list {
			out = append(out, toProviderJSON(p))
		}
		writeJSON(w, http.StatusOK, out)

	case http.MethodPost:
		var body struct {
			Name    string `json:"name"`
			Kind    string `json:"kind"`
			BaseURL string `json:"base_url"`
			APIKey  string `json:"api_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
			return
		}
		if body.Name == "" || body.Kind == "" || body.BaseURL == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("name, kind, base_url required"))
			return
		}
		p, err := s.store.UpsertProvider(body.Name, body.Kind, body.BaseURL, body.APIKey)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, toProviderJSON(*p))

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleProviderByName: GET 单查(脱敏),DELETE 删除。
func (s *Server) handleProviderByName(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/providers/")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		p, err := s.store.GetProviderByName(name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if p == nil {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, toProviderJSON(*p))
	case http.MethodDelete:
		if err := s.store.DeleteProvider(name); err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
```

> `newTestStore` 跨包问题:server_test 包若没法用 store 的 helper,就在 server_test 内单独定义一个 helper 调用 `store.Open(filepath.Join(t.TempDir(), "test.db"))`。或者在 Task 3 就把 `newTestStore` 放到 `store` 包(已导出函数 / 内部 helper),然后 server 测试用 `store.Open` 直接开。

**Step 4: Run test to verify it passes**

Run: `go test ./internal/server/ -run ProvidersAPI -v`
Expected: PASS。

**Step 5: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go
git commit -m "feat(server): /api/providers CRUD + apiKey 脱敏(Task 5a)"
```

---

### 5b. server live match state + `/api/matches` 启动接口

**Files:**
- Modify: `internal/server/server.go`(加 liveMatch struct、Server.liveMu/live、handleMatches)
- Modify: `internal/server/server_test.go`

**Step 1: Write the failing test**

```go
func TestMatchesAPI_StartRejectsBadRequests(t *testing.T) {
	rec := newTestStore(t)
	defer rec.Close()
	srv := New(rec, "")

	// 没有 provider:400
	body := strings.NewReader(`{"seats":[{"provider":"deepseek","model":"x"},{"provider":"deepseek","model":"y"}],"hands":2}`)
	req := httptest.NewRequest("POST", "/api/matches", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("want 400 for missing provider, got %d body=%s", w.Code, w.Body.String())
	}

	// 加一个 provider
	_, _ = rec.UpsertProvider("deepseek", "openai", "https://api.deepseek.com", "sk-x")

	// seat 数 < 2:400
	body = strings.NewReader(`{"seats":[{"provider":"deepseek","model":"x"}],"hands":2}`)
	req = httptest.NewRequest("POST", "/api/matches", body)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("want 400 for 1 seat, got %d", w.Code)
	}
}

// 注:真正能跑通的对局需要 provider 真的返回 LLM 响应,在 server 单测里不便。
// 端到端测试在 Task 8 或手工完成。
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run MatchesAPI -v`
Expected: FAIL(路由不存在)。

**Step 3: Write minimal implementation**

在 `internal/server/server.go` 修改 `Server` struct 与 `New`:

```go
type Server struct {
	store     *store.Store
	staticDir string
	mux       *http.ServeMux

	liveMu sync.Mutex
	live   *liveMatch
}

type liveMatch struct {
	id        string
	cancel    context.CancelFunc
	startedAt time.Time
	subsMu    sync.Mutex
	subs      map[chan match.LiveEvent]struct{}
	done      chan struct{} // RunLive 结束后关闭
}

func New(s *store.Store, staticDir string) *Server {
	srv := &Server{
		store:     s,
		staticDir: staticDir,
		mux:       http.NewServeMux(),
	}
	srv.routes()
	return srv
}
```

在 `routes()` 加:

```go
s.mux.HandleFunc("/api/matches", s.handleMatchesStart)           // POST
s.mux.HandleFunc("/api/matches/current", s.handleMatchCurrent)   // GET
s.mux.HandleFunc("/api/matches/current/stream", s.handleMatchStream) // GET SSE
s.mux.HandleFunc("/api/matches/current/stop", s.handleMatchStop) // POST
```

实现:

```go
// handleMatchesStart: POST /api/matches
// body: {seats:[{provider, model}], hands, seed?, sb?, bb?, starting_stack?}
func (s *Server) handleMatchesStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.liveMu.Lock()
	if s.live != nil {
		s.liveMu.Unlock()
		writeError(w, http.StatusConflict, fmt.Errorf("a match is already running"))
		return
	}
	s.liveMu.Unlock()

	var body struct {
		Seats []struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"seats"`
		Hands         int   `json:"hands"`
		Seed          int64 `json:"seed"`
		SmallBlind    int   `json:"sb"`
		BigBlind      int   `json:"bb"`
		StartingStack int   `json:"starting_stack"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	if len(body.Seats) < 2 || len(body.Seats) > 6 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("seats must be 2-6, got %d", len(body.Seats)))
		return
	}
	if body.Hands <= 0 {
		body.Hands = 20
	}
	if body.SmallBlind <= 0 {
		body.SmallBlind = 5
	}
	if body.BigBlind <= 0 {
		body.BigBlind = 10
	}
	if body.StartingStack <= 0 {
		body.StartingStack = 1000
	}

	// 查每个 provider,准备 PlayerSpec + LLMPlayer 工厂
	httpClient := providers.DefaultHTTPClient(0)
	var specs []match.PlayerSpec
	var makePlayers []func() engine.Player
	for i, seat := range body.Seats {
		pcfg, err := s.store.GetProviderByName(seat.Provider)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("lookup provider %q: %w", seat.Provider, err))
			return
		}
		if pcfg == nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("provider %q not found", seat.Provider))
			return
		}
		if pcfg.APIKey == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("provider %q has empty api_key", seat.Provider))
			return
		}
		specs = append(specs, match.PlayerSpec{
			Provider: seat.Provider,
			Model:    seat.Model,
			Label:    fmt.Sprintf("%s:%s", seat.Provider, seat.Model),
		})
		// capture loop vars
		kind, baseURL, apiKey, model := pcfg.Kind, pcfg.BaseURL, pcfg.APIKey, seat.Model
		makePlayers = append(makePlayers, func() engine.Player {
			p, err := providers.ByKind(kind, baseURL, apiKey, httpClient)
			if err != nil {
				// RunLive 会 panic,但我们有 LLMPlayer fallback fold;此处不处理,
				// 让 RunLive 期间的 panic 由上层的 recover 兜底(见 startLive)。
				return newPanicPlayer(err)
			}
			return &players.LLMPlayer{Provider: p, Model: model}
		})
		_ = i
	}

	matchID := fmt.Sprintf("m-%d", time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())

	s.liveMu.Lock()
	if s.live != nil {
		// 并发竞态:并发两个 POST 都过了第一道闸
		s.liveMu.Unlock()
		cancel()
		writeError(w, http.StatusConflict, fmt.Errorf("a match is already running"))
		return
	}
	s.live = &liveMatch{
		id:        matchID,
		cancel:    cancel,
		startedAt: time.Now(),
		subs:      map[chan match.LiveEvent]struct{}{},
		done:      make(chan struct{}),
	}
	lm := s.live
	s.liveMu.Unlock()

	// 启动 goroutine 跑 RunLive + 分发到 subs
	go s.runLiveAndDistribute(ctx, lm, specs, makePlayers, body.Hands, body.Seed,
		engine.Config{SmallBlind: body.SmallBlind, BigBlind: body.BigBlind, StartingStack: body.StartingStack})

	writeJSON(w, http.StatusOK, map[string]any{
		"match_id": matchID,
	})
}

// runLiveAndDistribute 跑 RunLive,把它发出的事件 fan-out 到所有订阅者。
func (s *Server) runLiveAndDistribute(
	ctx context.Context,
	lm *liveMatch,
	specs []match.PlayerSpec,
	makePlayers []func() engine.Player,
	hands int,
	seed int64,
	cfg engine.Config,
) {
	defer close(lm.done)
	// RunLive panic 兜底(避免 provider 工厂失败导致整个服务挂)
	defer func() {
		if r := recover(); r != nil {
			// 发 error 事件给订阅者
			ev := match.LiveEvent{Type: match.EvError}
			ev.Payload, _ = json.Marshal(map[string]any{"error": fmt.Sprintf("panic: %v", r)})
			s.broadcast(lm, ev)
		}
	}()

	out := make(chan match.LiveEvent, 256)
	doneRun := make(chan struct{})
	go func() {
		defer close(doneRun)
		defer close(out) // RunLive 结束后 close out
		_, _ = match.RunLive(ctx, specs, makePlayers, hands, cfg, seed, s.store, out)
	}()

	for ev := range out {
		s.broadcast(lm, ev)
	}
	<-doneRun

	// 清理 live 指针
	s.liveMu.Lock()
	if s.live == lm {
		s.live = nil
	}
	s.liveMu.Unlock()
}

// broadcast 把 ev 非阻塞地发给所有订阅者;sub 满则删除该 sub。
func (s *Server) broadcast(lm *liveMatch, ev match.LiveEvent) {
	lm.subsMu.Lock()
	defer lm.subsMu.Unlock()
	for ch := range lm.subs {
		select {
		case ch <- ev:
		default:
			// 慢客户端,丢事件(可选择关闭它,这里选择丢)
		}
	}
}

// handleMatchCurrent: GET /api/matches/current
func (s *Server) handleMatchCurrent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.liveMu.Lock()
	lm := s.live
	s.liveMu.Unlock()
	if lm == nil {
		writeJSON(w, http.StatusOK, map[string]any{"running": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"running":    true,
		"match_id":   lm.id,
		"started_at": lm.startedAt.Format(time.RFC3339),
	})
}

// handleMatchStop: POST /api/matches/current/stop
func (s *Server) handleMatchStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.liveMu.Lock()
	lm := s.live
	s.liveMu.Unlock()
	if lm == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("no running match"))
		return
	}
	lm.cancel()
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelling"})
}

// handleMatchStream 见 Task 5c。
```

`newPanicPlayer` 在 5b 文件顶部定义(临时占位,RunLive 期间被调用时直接 fold):

```go
// panicPlayer 用于 provider 工厂失败时占位,Decide 直接 fold。
type panicPlayer struct{ err error }

func newPanicPlayer(err error) *panicPlayer { return &panicPlayer{err: err} }
func (p *panicPlayer) Decide(obs engine.Observation) engine.Action {
	return engine.Action{Type: engine.Fold}
}
```

新增 imports:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"pokermind/internal/engine"
	"pokermind/internal/match"
	"pokermind/internal/players"
	"pokermind/internal/players/providers"
	"pokermind/internal/store"
)
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/server/ -v`
Expected: PASS。

**Step 5: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go
git commit -m "feat(server): /api/matches POST 启动 + live state + 广播(Task 5b)"
```

---

### 5c. SSE 流 `/api/matches/current/stream`

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/server_test.go`

**Step 1: Write the failing test**

```go
func TestMatchStream_NoRunning(t *testing.T) {
	rec := newTestStore(t)
	defer rec.Close()
	srv := New(rec, "")

	req := httptest.NewRequest("GET", "/api/matches/current/stream", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	// 没 running match 时应立刻返回 200 + 空 body 或 sentinel
	body := w.Body.String()
	if !strings.Contains(body, "no_running") {
		t.Errorf("body should contain no_running marker: %q", body)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run MatchStream -v`
Expected: FAIL。

**Step 3: Write minimal implementation**

`handleMatchStream`:

```go
// handleMatchStream: GET /api/matches/current/stream  (SSE)
//
// 行为:
//   - 若无 running match,写一条 event: no_running 后关闭。
//   - 若有,订阅 live.subs,逐条写 event:<type>\ndata:<json>\n\n。
//   - 客户端断开或 done 后关闭。
func (s *Server) handleMatchStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	s.liveMu.Lock()
	lm := s.live
	s.liveMu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if lm == nil {
		_, _ = fmt.Fprintf(w, "event: no_running\ndata: {}\n\n")
		flusher.Flush()
		return
	}

	// 订阅
	sub := make(chan match.LiveEvent, 256)
	lm.subsMu.Lock()
	lm.subs[sub] = struct{}{}
	lm.subsMu.Unlock()

	defer func() {
		lm.subsMu.Lock()
		delete(lm.subs, sub)
		lm.subsMu.Unlock()
	}()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-lm.done:
			// 把剩余 buffered 事件 flush 出去
			for {
				select {
				case ev := <-sub:
					writeSSE(w, ev)
					flusher.Flush()
				default:
					return
				}
			}
		case ev := <-sub:
			writeSSE(w, ev)
			flusher.Flush()
		}
	}
}

// writeSSE 写一个 SSE 帧:event: <type>\ndata: <json>\n\n
func writeSSE(w http.ResponseWriter, ev match.LiveEvent) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Payload)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/server/ -v`
Expected: PASS。

**Step 5: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go
git commit -m "feat(server): SSE /api/matches/current/stream(Task 5c)"
```

---

## Task 6: CLI .env 迁移 + newLLMPlayer 改读库

**Files:**
- Modify: `cmd/pokermind/main.go`

**Step 1: Write the failing test**

CLI 集成测试较重,这里改用回归式手工验证 + 一个 go test 跑 migrate 函数:

新建 `cmd/pokermind/migrate_providers_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"

	"pokermind/internal/store"
)

func TestMigrateProvidersFromEnv(t *testing.T) {
	// 注入 env
	t.Setenv("POKERMIND_DEEPSEEK_API_KEY", "sk-deepseek-xxx")
	t.Setenv("POKERMIND_DEEPSEEK_BASE_URL", "https://api.deepseek.com")
	t.Setenv("POKERMIND_GLM_API_KEY", "sk-glm-yyy")

	rec, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()

	if err := migrateProvidersFromEnv(rec); err != nil {
		t.Fatal(err)
	}

	ds, _ := rec.GetProviderByName("deepseek")
	if ds == nil || ds.APIKey != "sk-deepseek-xxx" || ds.Kind != "openai" {
		t.Errorf("deepseek not migrated: %+v", ds)
	}
	glm, _ := rec.GetProviderByName("glm")
	if glm == nil || glm.APIKey != "sk-glm-yyy" || glm.Kind != "openai" {
		t.Errorf("glm not migrated: %+v", glm)
	}

	// 幂等:再跑一遍不应覆盖
	t.Setenv("POKERMIND_DEEPSEEK_API_KEY", "sk-different")
	if err := migrateProvidersFromEnv(rec); err != nil {
		t.Fatal(err)
	}
	ds, _ = rec.GetProviderByName("deepseek")
	if ds.APIKey != "sk-deepseek-xxx" {
		t.Errorf("migrate should not overwrite; got %q", ds.APIKey)
	}
}

func TestMigrateProvidersFromEnv_NoEnv(t *testing.T) {
	os.Unsetenv("POKERMIND_DEEPSEEK_API_KEY")
	os.Unsetenv("POKERMIND_GLM_API_KEY")
	rec, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()
	if err := migrateProvidersFromEnv(rec); err != nil {
		t.Fatal(err)
	}
	list, _ := rec.ListProviders()
	if len(list) != 0 {
		t.Errorf("no env should give no providers, got %d", len(list))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/pokermind/ -run MigrateProviders -v`
Expected: FAIL `undefined: migrateProvidersFromEnv`。

**Step 3: Write minimal implementation**

在 `cmd/pokermind/main.go`:

1. 改造 `newLLMPlayer` 签名:

```go
// 改前:func newLLMPlayer(provider, model string, httpClient *http.Client) (*players.LLMPlayer, error)
// 改后:
func newLLMPlayer(rec *store.Store, providerName, model string, httpClient *http.Client) (*players.LLMPlayer, error) {
	cfg, err := rec.GetProviderByName(providerName)
	if err != nil {
		return nil, fmt.Errorf("query provider %q: %w", providerName, err)
	}
	if cfg == nil {
		return nil, fmt.Errorf("provider %q not found (configure at /#/providers or check .env)", providerName)
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("provider %q has empty api_key", providerName)
	}
	p, err := providers.ByKind(cfg.Kind, cfg.BaseURL, cfg.APIKey, httpClient)
	if err != nil {
		return nil, fmt.Errorf("provider %q kind %q: %w", providerName, cfg.Kind, err)
	}
	return &players.LLMPlayer{Provider: p, Model: model}, nil
}
```

2. 新增 `migrateProvidersFromEnv`:

```go
// migrateProvidersFromEnv 把 .env 里的 POKERMIND_DEEPSEEK_API_KEY 和 POKERMIND_GLM_API_KEY
// 一次性迁移到 store.providers 表。幂等(INSERT OR IGNORE 语义由 UpsertProvider + 查重实现)。
// 已在库里同名 provider 不覆盖。
func migrateProvidersFromEnv(rec *store.Store) error {
	type env struct {
		name, kind, defaultURL, keyEnv, urlEnv string
	}
	items := []env{
		{name: "deepseek", kind: "openai", defaultURL: "https://api.deepseek.com",
			keyEnv: "POKERMIND_DEEPSEEK_API_KEY", urlEnv: "POKERMIND_DEEPSEEK_BASE_URL"},
		{name: "glm", kind: "openai", defaultURL: "https://open.bigmodel.cn/api/paas/v4",
			keyEnv: "POKERMIND_GLM_API_KEY", urlEnv: "POKERMIND_GLM_BASE_URL"},
	}
	for _, it := range items {
		key := os.Getenv(it.keyEnv)
		if key == "" {
			continue // 没 env 就跳过
		}
		// 已存在则跳过
		existing, err := rec.GetProviderByName(it.name)
		if err != nil {
			return fmt.Errorf("migrate %s: %w", it.name, err)
		}
		if existing != nil {
			continue
		}
		url := envStr(it.urlEnv, it.defaultURL)
		if _, err := rec.UpsertProvider(it.name, it.kind, url, key); err != nil {
			return fmt.Errorf("migrate %s upsert: %w", it.name, err)
		}
	}
	return nil
}
```

3. 在 `main()` 里 `loadDotEnv(".env")` 之后、子命令解析前,加迁移调用。但 main 没有全局 store —— 子命令自己开 store。所以把迁移放到**每个需要 store 的子命令开头**:run、match、leaderboard、serve 里 `store.Open` 后立刻 `migrateProvidersFromEnv(rec)`。

4. 改 `runCmd` 和 `matchCmd`:
   - 都加 `--db` flag(默认 `pokermind.db`)。
   - 在 newLLMPlayer 前开 store,跑迁移。
   - 调用 `newLLMPlayer(rec, *provider, *model, httpClient)`。

例:`runCmd` 改后片段:

```go
func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	provider := fs.String("provider", "", "LLM provider name (configured at /#/providers)")
	model := fs.String("model", "", "model name")
	hands := fs.Int("hands", 1, "number of hands to play")
	seed := fs.Int64("seed", 1, "RNG seed")
	dbPath := fs.String("db", "pokermind.db", "SQLite path (for provider config)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *provider == "" || *model == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --provider and --model are required")
		os.Exit(2)
	}

	timeoutSec := envInt("POKERMIND_HTTP_TIMEOUT_SECONDS", 60)
	httpClient := providers.DefaultHTTPClient(timeoutSec)

	rec, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
	defer rec.Close()
	if err := migrateProvidersFromEnv(rec); err != nil {
		fmt.Fprintln(os.Stderr, "WARN migrate providers:", err)
	}

	llmPlayer, err := newLLMPlayer(rec, *provider, *model, httpClient)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}

	// ... 其余不变
}
```

`matchCmd` 同样改造:开 store、迁移、`newLLMPlayer(rec, p, m, httpClient)`。

`serveCmd` 在 `store.Open` 后加 `migrateProvidersFromEnv(rec)`,然后才 `server.New`。

**Step 4: Run tests to verify pass**

Run: `go test ./cmd/pokermind/ -run MigrateProviders -v`
Expected: PASS。

Run: `go build -o pokermind ./cmd/pokermind && ./pokermind --help`
Expected: 不崩,帮助正常显示。

**Step 5: Commit**

```bash
git add cmd/pokermind/main.go cmd/pokermind/migrate_providers_test.go
git commit -m "feat(cmd): newLLMPlayer 读库 + .env 自动迁移(Task 6)"
```

---

## Task 7: web 导航 + providers 页

**Files:**
- Modify: `web/index.html`(顶栏加链接)
- Modify: `web/app.js`(路由 + providers 视图)
- Modify: `web/style.css`(表单样式,可选)

**Step 1: 写页结构(无测试,纯前端,先写实现后人工验证)**

在 `web/index.html` 顶栏加两个链接:

```html
<header class="topbar">
  <a href="#/" class="brand">♠ PokerMind</a>
  <span class="tagline">把模型的「内心戏」摊开看</span>
  <nav class="nav">
    <a href="#/providers">配置</a>
    <a href="#/live">现场</a>
  </nav>
</header>
```

在 `web/app.js` 加路由分发 + providers 视图渲染:

```js
// 在 router 里加分支
async function route(path) {
  const app = document.getElementById('app');
  if (path === '' || path === '/') { return renderGameList(app); }
  if (path.startsWith('/game/'))   { return renderGameDetail(app, path.slice('/game/'.length)); }
  if (path === '/providers')       { return renderProviders(app); }
  if (path === '/live')            { return renderLiveConfig(app); }
  if (path.startsWith('/live/'))   { return renderLiveWatch(app, path.slice('/live/'.length)); }
  app.innerHTML = '<p>Not found.</p>';
}

// === providers 页 ===
async function renderProviders(app) {
  app.innerHTML = `
    <section class="page">
      <h2>LLM Providers</h2>
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
  const r = await fetch('/api/providers');
  const list = await r.json();
  tbody.innerHTML = (list || []).map(p => `
    <tr>
      <td>${p.name}</td><td>${p.kind}</td><td>${p.base_url}</td><td>${p.api_key}</td>
      <td><button data-name="${p.name}" class="del">删</button></td>
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
```

CSS(追加到 `style.css`):

```css
.topbar .nav { margin-left: auto; display: flex; gap: 12px; }
.topbar .nav a { color: #fff; text-decoration: none; font-size: 14px; }
#prov-table { width: 100%; border-collapse: collapse; margin: 12px 0; }
#prov-table th, #prov-table td { border: 1px solid #ddd; padding: 6px 10px; text-align: left; }
#prov-form { display: flex; flex-wrap: wrap; gap: 8px; }
#prov-form input, #prov-form select { padding: 6px; }
```

**Step 2: 手工验证**

```bash
go build -o pokermind ./cmd/pokermind
./pokermind serve
```

浏览器开 `http://localhost:8080/#/providers`,能增删查 provider。`api_key` 列显示 `***xxxx`。

**Step 3: Commit**

```bash
git add web/index.html web/app.js web/style.css
git commit -m "feat(web): 导航 + providers 配置页(Task 7)"
```

---

## Task 8: web live 配置页 + 观战页

**Files:**
- Modify: `web/app.js`
- Modify: `web/style.css`(观战桌样式,可扩展现有 .seat)

**Step 1: 实现 live 配置页 + 观战页**

在 `web/app.js` 追加:

```js
// === live 配置页 ===
async function renderLiveConfig(app) {
  const r = await fetch('/api/providers');
  const provs = await r.json();
  const optStr = (provs || []).map(p => `<option value="${p.name}">${p.name} (${p.kind})</option>`).join('');

  app.innerHTML = `
    <section class="page">
      <h2>现场对战</h2>
      <div id="live-status"></div>
      <form id="live-form">
        <label>座位数 <select name="n" id="seat-n">
          <option>2</option><option>3</option><option>4</option><option>5</option><option>6</option>
        </select></label>
        <label>手数 <input name="hands" value="20" type="number" min="1"></label>
        <label>seed <input name="seed" value="" type="number" placeholder="随机"></label>
        <label>SB <input name="sb" value="5" type="number"></label>
        <label>BB <input name="bb" value="10" type="number"></label>
        <label>起手筹码 <input name="starting_stack" value="1000" type="number"></label>
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

  // 如果有 running match,提示
  const stReq = await fetch('/api/matches/current');
  const st = await stReq.json();
  if (st.running) {
    document.getElementById('live-status').innerHTML = `
      <p>当前有对局在跑:<a href="#/live/${st.match_id}">前往观战 →</a></p>
    `;
  }

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

// === live 观战页 ===
function renderLiveWatch(app, matchID) {
  app.innerHTML = `
    <section class="page">
      <header>
        <h2>现场观战 <small id="match-progress"></small></h2>
        <button id="stop-btn">停止对局</button>
      </header>
      <div class="poker-table" id="table"></div>
      <div class="community" id="community"></div>
      <div class="pot" id="pot">底池: 0</div>
      <ul class="action-log" id="log"></ul>
    </section>
  `;

  let totalHands = 0;
  let currentHand = 0;
  const seats = {}; // seat -> {label, stack, bet, hole, folded}

  document.getElementById('stop-btn').onclick = async () => {
    if (!confirm('停止当前对局?(不会落库)')) return;
    await fetch('/api/matches/current/stop', {method: 'POST'});
  };

  const es = new EventSource('/api/matches/current/stream');
  es.addEventListener("no_running", () => {
    document.getElementById('log').innerHTML += `<li class="sys">没有正在运行的对局</li>`;
    es.close();
  });
  es.addEventListener("match_started", e => {
    const d = JSON.parse(e.data);
    totalHands = d.hands;
    renderTable(d.seats);
  });
  es.addEventListener("hand_started", e => {
    const d = JSON.parse(e.data);
    currentHand = d.hand;
    document.getElementById('match-progress').textContent = `第 ${d.hand}/${totalHands} 手`;
    // 重置每 seat 的本手下注 / 公共牌 / 底池(下注归入 pot 由 action 累加)
    Object.values(seats).forEach(s => { s.bet = 0; s.folded = false; });
    // 这里不揭示对手底牌(留给 showdown)
    for (const h of (d.holes || [])) {
      if (seats[h.seat]) seats[h.seat].hole = h.cards;
    }
    refreshView();
  });
  es.addEventListener("action", e => {
    const d = JSON.parse(e.data);
    const s = seats[d.seat];
    if (!s) return;
    let line = `seat${d.seat} ${d.provider}:${d.model} ${d.action_type.toUpperCase()}`;
    if (d.action_type === 'raise') line += `-to-${d.amount}`;
    if (d.has_report) line += ` — "${d.reasoning}" (hs=${d.hand_strength} eq=${d.estimated_equity} bluff=${d.is_bluffing})`;
    document.getElementById('log').innerHTML += `<li>${line}</li>`;

    // 更新 bet / pot(前端累加)
    if (d.action_type === 'fold') { s.folded = true; }
    if (d.action_type === 'call' || d.action_type === 'raise') {
      // 简化:用 amount 作为本手该 seat 的累计投入(raise-to 是总数)
      // call 时 amount 是 0(engine.Action.Amount 在 call 时为 0),要正确累加 pot
      // 需另给 toCall —— 但我们决定不在 payload 带;前端只能用 raise 的 amount
      // 所以 call/check 时 pot 不累加(会在 hand_finished 的 pot 字段校正)
    }
    if (d.action_type === 'raise') {
      s.bet = d.amount;
    }
    refreshView();
  });
  es.addEventListener("hand_finished", e => {
    const d = JSON.parse(e.data);
    // 用 d.pot 校正底池
    document.getElementById('pot').textContent = `底池: ${d.pot}`;
    // 显示公共牌
    document.getElementById('community').innerHTML = (d.community || []).map(cardStr).join(' ');
    // 标记赢家
    for (const seat of d.winners) {
      if (seats[seat]) seats[seat].winner = true;
    }
    document.getElementById('log').innerHTML += `<li class="sys">第 ${d.hand} 手结束:赢家 seat ${d.winners.join(',')},pot=${d.pot}</li>`;
    refreshView();
  });
  es.addEventListener("match_finished", e => {
    const d = JSON.parse(e.data);
    es.close();
    const summary = d.final_stacks.map((c, i) => `seat${i}=${c}`).join(', ');
    document.getElementById('log').innerHTML += `<li class="sys">对局结束:赢家 seat ${d.winner_seat},game_id=${d.game_id}<br>${summary}</li>`;
    document.getElementById('stop-btn').disabled = true;
  });
  es.addEventListener("error", e => {
    document.getElementById('log').innerHTML += `<li class="sys">错误事件</li>`;
  });
  es.onerror = () => {
    // 浏览器自动重连;若 match 已 finished,服务端会写 no_running,close
  };

  function renderTable(seatList) {
    const tbl = document.getElementById('table');
    tbl.innerHTML = seatList.map(s => {
      seats[s.seat] = {label: `${s.provider}:${s.model}`, stack: 0, bet: 0, hole: [], folded: false, winner: false};
      return `<div class="seat" id="seat-${s.seat}"></div>`;
    }).join('');
    refreshView();
  }
  function refreshView() {
    for (const [id, s] of Object.entries(seats)) {
      const el = document.getElementById('seat-' + id);
      if (!el) continue;
      el.innerHTML = `
        <div class="label">${s.label}</div>
        <div class="stack">筹码: ${s.stack}</div>
        <div class="bet">本手投入: ${s.bet}</div>
        <div class="hole">${(s.hole || []).map(cardStr).join(' ') || '—'}</div>
        ${s.folded ? '<div class="flag">FOLD</div>' : ''}
        ${s.winner ? '<div class="flag win">WIN</div>' : ''}
      `;
    }
  }
  function cardStr(c) { return `<span class="card">${c}</span>`; }
}
```

CSS 追加到 `web/style.css`:

```css
.poker-table { display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px; margin: 12px 0; }
.seat { border: 1px solid #999; border-radius: 6px; padding: 10px; background: #f8f8f8; }
.seat .label { font-weight: bold; }
.seat .flag { color: #c00; font-weight: bold; }
.seat .flag.win { color: #080; }
.community { min-height: 40px; padding: 8px; border: 1px dashed #bbb; }
.pot { font-weight: bold; margin: 6px 0; }
.action-log { list-style: none; padding: 0; max-height: 300px; overflow-y: auto; border-top: 1px solid #ddd; }
.action-log li { padding: 4px 6px; border-bottom: 1px solid #eee; font-family: monospace; }
.action-log li.sys { background: #fffbe6; }
```

**Step 2: 端到端验证**

```bash
go build -o pokermind ./cmd/pokermind
./pokermind serve
```

在浏览器:
1. `/#/providers` 配一个 openai 兼容 provider(如 deepseek,填真实 key)。
2. `/#/live` 选 2 seat,都选这个 provider,各填不同 model(比如 `deepseek-v4-flash`、`deepseek-v4-pro`)。
3. 点「开始对局」,跳到观战页。
4. 观察:逐动作出现,底牌/筹码/底池更新。
5. 对局结束后,回 `/#/` 看到新对局入列表,点进去能完整回放。

**Step 3: Commit**

```bash
git add web/app.js web/style.css
git commit -m "feat(web): live 配置页 + SSE 观战页(Task 8)"
```

---

## Task 9: 端到端验证 + 文档更新

**Step 1: 跑全套测试**

```bash
go test ./...
```

Expected: 全 PASS。

**Step 2: 端到端手测**(同 Task 8 Step 2)

覆盖以下路径:
- 旧 CLI 仍可用:`./pokermind match --players deepseek:...,glm:... --hands 5`(需 .env 有 key)
- web 配 provider → live 对局 → 观战 → 完整落库 → 回放
- web 点「停止」中断对局,确认不落库、`/api/matches/current` 回 `running:false`
- 第二次开对局(在第一次完成后)正常启动

**Step 3: 更新 README**

把 `.env.example` 新增 `POKERMIND_HTTP_TIMEOUT_SECONDS` 之外不动(env 仍兼容)。README 的"环境变量"表后追加一段"Web 配置":

```markdown
### Provider 配置

启动 `pokermind serve` 后,在浏览器 `http://localhost:8080/#/providers` 增改 provider:
- **name**:唯一标识,CLI `--provider <name>` 与 web 选座位时引用此名
- **kind**:`openai`(OpenAI 兼容,DeepSeek/GLM/Qwen 等绝大多数)或 `anthropic`(Claude)
- **base_url**:不含末尾 `/`;如 `https://api.deepseek.com`、`https://api.anthropic.com`
- **api_key**:存 SQLite 明文,API 响应里脱敏成 `***1234`

CLI 启动时自动把 `.env` 里的 `POKERMIND_DEEPSEEK_API_KEY`/`POKERMIND_GLM_API_KEY` 迁移成同名 provider(已存在则不覆盖)。

### 现场观战

`http://localhost:8080/#/live` 选 N 个 seat 的 provider/model → 点开始 → 跳到 `#/live/{match_id}` 通过 SSE 逐动作观战。结束自动落库,可在 `#/` 重看。
```

**Step 4: Commit**

```bash
git add README.md .env.example  # 若 .env.example 也更新
git commit -m "docs: README 加 provider 配置 + 现场观战说明(Task 9)"
```

---

## Done

- 全套 `go test ./...` 通过
- 端到端手测通过(CLI 旧用法 + web 新流程)
- 9 个 commit,每个独立可回滚

## 已知遗留(不在本计划)

- 多对局并行(目前同时一个 live match)
- SSE 客户端重连后历史事件补发
- anthropic-version 升级(目前写死 2023-06-01)
- 加密存 apiKey
