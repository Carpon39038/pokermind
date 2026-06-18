# PokerMind 开发进度

> 最近更新:2026-06-18

## 总体里程碑

| 阶段 | 状态 | 说明 |
|---|---|---|
| **M0** 引擎地基 + 单模型调通 | ✅ 完成 | card/evaluator/hand/Player/RuleBot/LLMPlayer 全到位,真实 LLM 能打一手 |
| **M1** 多模型对局 + 内心戏落库 | ✅ 完成 | SQLite 4 表落库、ELO、CLI `match`,跑通真实对打 |
| **M2** Web 牌桌 + 逐手回放 | ✅ 完成 | `serve` 子命令,局列表 + 回放页 + 具象扑克牌 + 内心戏气泡 |
| **M3** 扩展 | 🚧 进行中 | 当前聚焦 6-max 多人桌;复式发牌 / 决策质量评分 / 更多 provider 未开工 |

## M3-6max 子进度(当前主线)

N 人桌(2-6)引擎 + 全边池,严格 TDD 推进。

| Task | 状态 | 内容 |
|---|---|---|
| 1. sidepot 纯算法 | ✅ | 多级边池 Compute + Distribute,10 测试 |
| 2a+2b. engine 类型泛化 | ✅ | `[2]` → slice,breaking,111 测试作 N=2 回归通过 |
| 2c+2d. runStreet N 人 | ✅ | 行动顺序轮转、acted 清空、弃牌结算,N=3 测试通过 |
| **2e. 多人摊牌 sidepot 集成** | ⏳ 下一步 | `settleShowdown` 重写为 N 人 + 走 sidepot |
| 2f. N=3 完整对局验证 | ⏳ | RuleBot N=3 跑到摊牌 |
| 3. match N 人 | ⏳ | `match` 接受 N 个 PlayerSpec |
| 4. store schema N 人 | ⏳ | 加 `num_seats` + `player_holes` JSON |
| 5. CLI + web N 人 | ⏳ | `--players "p1,p2,p3"`,web 多座位布局 |

## 测试与质量

- **113 个单测全绿**(engine 57 / sidepot 10 / store 10 / match 5 / server 7 / players 13 / providers 4 / elo 5 + 若干编译期断言)
- `go vet ./...` 干净,`go build ./...` 干净
- 真实端到端跑通:DeepSeek-v4-flash vs deepseek-v4-pro 10 手对局已落库并 Web 可回放

## 当前架构

```
cmd/pokermind/        CLI 入口(run / match / leaderboard / serve)
internal/
  engine/             纯引擎:card / evaluator / hand / rulebot(N 人下注轮已支持)
  players/            LLMPlayer(prompt + JSON 解析 + 重试 + fallback)
    providers/        OpenAI 兼容 HTTP adapter(DeepSeek / 智谱 GLM 共用)
  match/              多手编排 + ELO 更新 + 落库
  store/              SQLite(modernc.org/sqlite,免 CGO)+ 查询
  elo/                标准 ELO 算法
  sidepot/            多级边池算法(M3-6max 已用)
  server/             net/http JSON API + 静态文件
web/                  原生 HTML/CSS/JS 单页(局列表 + 回放 + 具象扑克牌)
docs/                 PLAN.md + 各阶段设计与实现计划 + 本文件
```

## 关键设计决定(已固化的)

- **引擎纯 stdlib**,与 LLM / HTTP / DB 完全解耦,可单测
- **Player 接口只一个 Decide 方法**,闭包通过 `PlayerFromFunc` 适配
- **Action 带 SelfReport**(内心戏)从模型返回那一刻就一起流到 DB(项目核心差异点)
- **SQLite 用 modernc.org/sqlite**(纯 Go 免 CGO),`SetMaxOpenConns(1)` 串行写
- **一局一事务**落库,失败回滚
- **ELO 标准 K=32 初始 1500**,跨局累积
- **引擎不感知跨手 stack 累积**,由 match 层用 `HandResult.FinalStacks` 注入下一手
- **非 OpenAI 兼容的 provider 单独适配**,OpenAI 兼容的共用一个 adapter

## 已知限制(本期接受)

- 复式发牌未做(运气噪声让 ELO 短期不可信,多跑几局缓解)
- 真实胜率(蒙特卡洛)未算,只存模型自报胜率
- 决策质量评分层(校准 / 失误率 / 雷达图 / GTO 对照)未做
- 多人摊牌结算暂未集成 sidepot(M3-6max Task 2e 进行中)
- Web UI 无实时直播,只做事后回放
