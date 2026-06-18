package main

import (
	"bufio"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"pokermind/internal/engine"
	"pokermind/internal/match"
	"pokermind/internal/players"
	"pokermind/internal/players/providers"
	"pokermind/internal/server"
	"pokermind/internal/store"
)

func main() {
	// 启动时自动加载 .env(若存在)。已在环境中显式 export 的变量优先,
	// 文件里的同名值不会覆盖。
	loadDotEnv(".env")

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:])
	case "match":
		matchCmd(os.Args[2:])
	case "leaderboard":
		leaderboardCmd(os.Args[2:])
	case "serve":
		serveCmd(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `pokermind — multi-LLM Texas Hold'em arena

Usage:
  pokermind run --provider <deepseek|glm> --model <name> [options]
      Play N hands of <LLM> vs RuleBot, print each action + reasoning.

  pokermind match --p1 <provider:model> --p2 <provider:model> [options]
      Play a Heads-up match between two models, persist to SQLite, update ELO.

  pokermind leaderboard [--db path]
      Print current ELO leaderboard from the database.

  pokermind serve [--addr :8080] [--db path] [--web dir]
      Start the web UI (game replay viewer) on http://localhost:8080.

run options:
  --provider    deepseek or glm (required)
  --model       e.g. deepseek-v4-flash, deepseek-v4-pro, glm-4.6 (required)
  --hands       Number of hands (default 1)
  --seed        RNG seed (default 1)

match options:
  --p1, --p2    player spec as provider:model (e.g. deepseek:deepseek-v4-flash)
  --hands       hands per match (default 100)
  --seed        RNG seed (default 1)
  --db          SQLite path (default pokermind.db)
  --verbose     print every LLM action's reasoning (default: only summaries)

Env (see .env.example):
  POKERMIND_DEEPSEEK_API_KEY / POKERMIND_DEEPSEEK_BASE_URL
  POKERMIND_GLM_API_KEY      / POKERMIND_GLM_BASE_URL
  POKERMIND_HTTP_TIMEOUT_SECONDS (default 60)`)
}

// newLLMPlayer 按 provider+model 构造一个 *players.LLMPlayer。
// provider 缺 key/未知时返回 error。
func newLLMPlayer(provider, model string, httpClient *http.Client) (*players.LLMPlayer, error) {
	var baseURL, apiKey string
	switch provider {
	case "deepseek":
		baseURL = envStr("POKERMIND_DEEPSEEK_BASE_URL", "https://api.deepseek.com")
		apiKey = mustEnv("POKERMIND_DEEPSEEK_API_KEY")
	case "glm":
		baseURL = envStr("POKERMIND_GLM_BASE_URL", "https://open.bigmodel.cn/api/paas/v4")
		apiKey = mustEnv("POKERMIND_GLM_API_KEY")
	default:
		return nil, fmt.Errorf("unknown provider %q (want deepseek or glm)", provider)
	}
	return &players.LLMPlayer{
		Provider: &providers.OpenAICompatProvider{
			BaseURL: baseURL,
			APIKey:  apiKey,
			HTTP:    httpClient,
		},
		Model: model,
	}, nil
}

// parsePlayerSpec 把 "deepseek:deepseek-v4-flash" 拆成 (provider, model)。
func parsePlayerSpec(spec string) (provider, model string, err error) {
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid player spec %q (want provider:model)", spec)
	}
	return parts[0], parts[1], nil
}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	provider := fs.String("provider", "", "LLM provider: deepseek or glm")
	model := fs.String("model", "", "model name")
	hands := fs.Int("hands", 1, "number of hands to play")
	seed := fs.Int64("seed", 1, "RNG seed")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *provider == "" || *model == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --provider and --model are required")
		os.Exit(2)
	}

	timeoutSec := envInt("POKERMIND_HTTP_TIMEOUT_SECONDS", 60)
	httpClient := providers.DefaultHTTPClient(timeoutSec)

	llmPlayer, err := newLLMPlayer(*provider, *model, httpClient)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}

	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	button := 0 // LLM 是 button(SB),RuleBot 是 BB

	for h := 1; h <= *hands; h++ {
		fmt.Printf("\n========== Hand #%d (seed=%d) ==========\n", h, *seed+int64(h-1))
		rng := rand.New(rand.NewSource(*seed + int64(h-1)))

		seats := [2]engine.PlayerSeat{
			{ID: 0, Stack: cfg.StartingStack, Player: llmPlayer},
			{ID: 1, Stack: cfg.StartingStack, Player: engine.RuleBot{}},
		}
		events, result := engine.PlayHand(seats, button, cfg, rng, h)
		for _, ev := range events {
			printEvent(ev)
		}
		fmt.Printf("\n--- Result: winner=%v pot=%d folded=%v\n", result.Winners, result.PotWon, result.Folded)
		if result.Showdown != nil {
			for seat, r := range result.Showdown.Ranks {
				fmt.Printf("    seat%d: %s\n", seat, r.String())
			}
		}
		button = 1 - button
		time.Sleep(500 * time.Millisecond) // 给 provider 喘息,避免限速
	}
}

// matchCmd: pokermind match --p1 provider:model --p2 provider:model [--hands N] [--seed S] [--db path] [--verbose]
func matchCmd(args []string) {
	fs := flag.NewFlagSet("match", flag.ExitOnError)
	p1Spec := fs.String("p1", "", "player 1 spec provider:model")
	p2Spec := fs.String("p2", "", "player 2 spec provider:model")
	hands := fs.Int("hands", 100, "hands per match")
	seed := fs.Int64("seed", 1, "RNG seed")
	dbPath := fs.String("db", "pokermind.db", "SQLite path")
	verbose := fs.Bool("verbose", false, "print every LLM action's reasoning")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *p1Spec == "" || *p2Spec == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --p1 and --p2 are required (provider:model)")
		os.Exit(2)
	}

	p1Prov, p1Model, err := parsePlayerSpec(*p1Spec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(2)
	}
	p2Prov, p2Model, err := parsePlayerSpec(*p2Spec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(2)
	}

	timeoutSec := envInt("POKERMIND_HTTP_TIMEOUT_SECONDS", 60)
	httpClient := providers.DefaultHTTPClient(timeoutSec)

	// makePlayer 工厂:每次 match.Play 调用前生成新的 LLMPlayer(避免共享状态)
	makePlayer := func(provider, model string) func() engine.Player {
		return func() engine.Player {
			p, err := newLLMPlayer(provider, model, httpClient)
			if err != nil {
				fmt.Fprintln(os.Stderr, "ERROR:", err)
				os.Exit(1)
			}
			return p
		}
	}

	rec, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
	defer rec.Close()

	spec1 := match.PlayerSpec{Provider: p1Prov, Model: p1Model, Label: p1Model}
	spec2 := match.PlayerSpec{Provider: p2Prov, Model: p2Model, Label: p2Model}

	fmt.Printf("=== Match: %s vs %s, %d hands, seed=%d ===\n", spec1.Label, spec2.Label, *hands, *seed)
	started := time.Now()

	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}
	res, err := match.Play(spec1, spec2, makePlayer(p1Prov, p1Model), makePlayer(p2Prov, p2Model), *hands, cfg, rec, *seed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}

	elapsed := time.Since(started).Round(time.Second)
	winnerLabel := "draw"
	switch res.Winner {
	case 0:
		winnerLabel = spec1.Label
	case 1:
		winnerLabel = spec2.Label
	}
	fmt.Printf("\n=== Match over: winner=%s, %d hands played, elapsed=%v ===\n", winnerLabel, res.HandsPlayed, elapsed)
	fmt.Printf("    final chips: %s=%d  %s=%d\n", spec1.Label, res.FinalStacks[0], spec2.Label, res.FinalStacks[1])
	fmt.Printf("    ELO change:  %s=%+d  %s=%+d\n", spec1.Label, int(res.EloChange[0]), spec2.Label, int(res.EloChange[1]))

	printLeaderboard(rec)
	_ = verbose // verbose 模式留待后续在 match 包加 hook
}

// leaderboardCmd: pokermind leaderboard [--db path]
func leaderboardCmd(args []string) {
	fs := flag.NewFlagSet("leaderboard", flag.ExitOnError)
	dbPath := fs.String("db", "pokermind.db", "SQLite path")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	rec, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
	defer rec.Close()
	printLeaderboard(rec)
}

// serveCmd: pokermind serve [--addr :8080] [--db path] [--web dir]
func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "listen address")
	dbPath := fs.String("db", "pokermind.db", "SQLite path")
	webDir := fs.String("web", "web", "static files directory (absolute or relative to cwd)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	rec, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
	defer rec.Close()

	srv := server.New(rec, *webDir)
	fmt.Printf("PokerMind web UI: http://localhost%s/  (db=%s, web=%s)\n", *addr, *dbPath, *webDir)
	fmt.Println("Ctrl-C 退出。")
	if err := http.ListenAndServe(*addr, srv); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

// printLeaderboard 打印 ELO 排行榜。
func printLeaderboard(rec *store.Store) {
	lb, err := rec.Leaderboard()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR reading leaderboard:", err)
		return
	}
	if len(lb) == 0 {
		fmt.Println("(no players yet)")
		return
	}
	fmt.Println("\n--- Leaderboard ---")
	fmt.Printf("%-24s %6s %6s %6s %10s\n", "player", "elo", "games", "wins", "net_chips")
	for _, r := range lb {
		fmt.Printf("%-24s %6d %6d %6d %10d\n", r.Label, r.Elo, r.Games, r.Wins, r.NetChips)
	}
}

// printEvent 把单个事件打印成可读行。
func printEvent(ev engine.Event) {
	switch ev.Type {
	case engine.BlindPosted:
		blind := "blind"
		if strings.Contains(ev.Message, "small") {
			blind = "small blind"
		} else if strings.Contains(ev.Message, "big") {
			blind = "big blind"
		}
		fmt.Printf("seat%d posts %s %d\n", ev.Seat, blind, ev.Amount)
	case engine.DealtHole:
		fmt.Printf("seat%d hole: %s\n", ev.Seat, cardsStr(ev.Cards))
	case engine.ActionTaken:
		printAction(ev)
	case engine.StreetAdvanced:
		if len(ev.Cards) > 0 {
			fmt.Printf("\n--- %s: %s ---\n", ev.Street, cardsStr(ev.Cards))
		} else if ev.Street == engine.Showdown {
			fmt.Printf("\n--- showdown ---\n")
		}
	case engine.PotAwarded:
		fmt.Printf("pot %d awarded to seat%v\n", ev.Amount, ev.Winners)
	case engine.HandFinished:
		kind := "showdown"
		if ev.Folded {
			kind = "fold"
		}
		fmt.Printf("hand finished (%s), winner=seat%v\n", kind, ev.Winners)
	default:
		fmt.Printf("event type=%d seat=%d\n", ev.Type, ev.Seat)
	}
}

// printAction 打印一个动作(带 LLM 的内心戏,如果有)。
func printAction(ev engine.Event) {
	if ev.Action == nil {
		fmt.Printf("seat%d (no action)\n", ev.Seat)
		return
	}
	a := ev.Action
	who := fmt.Sprintf("seat%d", ev.Seat)
	if a.SelfReport != nil {
		who = fmt.Sprintf("LLM(seat%d)", ev.Seat)
	} else {
		who = fmt.Sprintf("RuleBot(seat%d)", ev.Seat)
	}
	switch a.Type {
	case engine.Fold:
		fmt.Printf("%s FOLD", who)
	case engine.Call:
		fmt.Printf("%s CALL", who)
	case engine.Raise:
		fmt.Printf("%s RAISE-to-%d", who, a.Amount)
	}
	if a.SelfReport != nil {
		sr := a.SelfReport
		fmt.Printf(" — %q (hs=%.2f eq=%.2f bluff=%v)", sr.Reasoning, sr.HandStrength, sr.EstimatedEquity, sr.IsBluffing)
	}
	fmt.Println()
}

func cardsStr(cs []engine.Card) string {
	if len(cs) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, c.String())
	}
	return strings.Join(parts, " ")
}

// loadDotEnv 从 path 加载 KEY=VALUE 到 os.Environ。
// 已在环境中存在的变量优先,文件不覆盖。文件缺失时静默(可选配置)。
// 支持:空行/# 注释跳过;值可加单/双引号去除引号;行内 # 不当注释(只在行首)。
// 不支持 export 前缀、变量插值、多行值 —— 刻意保持极简,stdlib only。
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // 文件不存在是合法状态
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 去掉可选的 "export " 前缀
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// 去掉包围引号
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}

// mustEnv 读必需的环境变量,缺失时退出。
func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "ERROR: env %s is required (see .env.example)\n", key)
		os.Exit(1)
	}
	return v
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
