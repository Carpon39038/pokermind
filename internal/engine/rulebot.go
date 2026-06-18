package engine

// RuleBot 是极简规则基线:牌力 >= Pair 就 call/check,否则 fold。
// preflop 仅看底牌是否口袋对;后续街用 Evaluate 判定。
// 永不主动 raise。用作 LLM 对比的基线对手与引擎单测。
type RuleBot struct{}

// Decide 实现 Player 接口。
func (RuleBot) Decide(obs Observation) Action {
	// 有公共牌时,评估 hole+community 的最强 5 张
	if len(obs.Community) > 0 {
		all := append(append([]Card{}, obs.HoleCards...), obs.Community...)
		// Evaluate 需 5-7 张;flop 后 hole(2)+community(>=3) = >=5
		if len(all) >= 5 {
			rank := Evaluate(all)
			if rank.Category >= Pair {
				return Action{Type: Call}
			}
		}
	} else {
		// preflop:看底牌是否口袋对
		if len(obs.HoleCards) == 2 && obs.HoleCards[0].Rank == obs.HoleCards[1].Rank {
			return Action{Type: Call}
		}
	}
	// 没足够牌力:ToCall=0 时 check(免费),否则 fold
	if obs.ToCall == 0 {
		return Action{Type: Call}
	}
	return Action{Type: Fold}
}
