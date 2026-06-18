package main

import (
	"bufio"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"pokermind/internal/engine"
	"pokermind/internal/players"
	"pokermind/internal/players/providers"
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
  pokermind run --provider <deepseek|glm> --model <model-name> [options]

Options:
  --provider    LLM provider: deepseek or glm (required)
  --model       Model name (e.g. deepseek-v4-flash, deepseek-v4-pro, glm-4.6) (required)
  --hands       Number of hands to play (default 1)
  --seed        RNG seed for reproducible dealing (default 1)

Env (see .env.example):
  POKERMIND_DEEPSEEK_API_KEY / POKERMIND_DEEPSEEK_BASE_URL
  POKERMIND_GLM_API_KEY      / POKERMIND_GLM_BASE_URL
  POKERMIND_HTTP_TIMEOUT_SECONDS (default 60)`)
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

	var llmPlayer *players.LLMPlayer
	switch *provider {
	case "deepseek":
		llmPlayer = &players.LLMPlayer{
			Provider: &providers.OpenAICompatProvider{
				BaseURL: envStr("POKERMIND_DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
				APIKey:  mustEnv("POKERMIND_DEEPSEEK_API_KEY"),
				HTTP:    httpClient,
			},
			Model: *model,
		}
	case "glm":
		llmPlayer = &players.LLMPlayer{
			Provider: &providers.OpenAICompatProvider{
				BaseURL: envStr("POKERMIND_GLM_BASE_URL", "https://open.bigmodel.cn/api/paas/v4"),
				APIKey:  mustEnv("POKERMIND_GLM_API_KEY"),
				HTTP:    httpClient,
			},
			Model: *model,
		}
	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown provider %q (want deepseek or glm)\n", *provider)
		os.Exit(2)
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
