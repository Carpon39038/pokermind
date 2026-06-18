// Package sidepot 实现多人德州扑克的边池(side pot)计算。
//
// 给定每个玩家本手总投入与是否争胜负,算出多级 pot 的层级与每层赢家集合。
// 规则:
//   - 弃牌者的贡献保留在池里,但他不参与任何层的争胜
//   - 每个"层级阈值"(level)由各玩家 contribution 的去重升序集合定义
//   - 每层 pot 金额 = (本层与上层阈值之差) × (contribution ≥ 本层阈值的人数)
//   - 每层赢家资格 = contribution ≥ 本层阈值 且 未弃牌 的玩家
//   - 平局时余数(不能整除的部分)给顺位最先(按 seat 升序)
package sidepot

import "sort"

// Pot 是一个边池层级。
type Pot struct {
	Amount   int   // 该层 pot 金额
	Eligible []int // 有资格争该层的玩家 seat 索引(按 seat 升序)
}

// Compute 按各玩家总投入(contrib)与是否争胜负(contending)计算多级边池。
// 返回的 Pot 列表按层级升序(主池在前,边池在后)。零投入或全员弃牌返回 nil。
//
// 参数长度必须相等,否则 panic(程序员错误)。
func Compute(contrib []int, contending []bool) []Pot {
	if len(contrib) != len(contending) {
		panic("sidepot: contrib and contending length mismatch")
	}
	n := len(contrib)
	if n == 0 {
		return nil
	}
	// 收集去重升序的阈值(> 0 的 contribution)
	levelSet := map[int]struct{}{}
	for _, c := range contrib {
		if c > 0 {
			levelSet[c] = struct{}{}
		}
	}
	if len(levelSet) == 0 {
		return nil
	}
	levels := make([]int, 0, len(levelSet))
	for lv := range levelSet {
		levels = append(levels, lv)
	}
	sort.Ints(levels)

	var pots []Pot
	prev := 0
	for _, lv := range levels {
		diff := lv - prev
		if diff <= 0 {
			continue
		}
		contribCount := 0
		var eligible []int
		for i := 0; i < n; i++ {
			if contrib[i] >= lv {
				contribCount++
				if contending[i] {
					eligible = append(eligible, i)
				}
			}
		}
		amount := diff * contribCount
		if amount > 0 {
			pots = append(pots, Pot{Amount: amount, Eligible: eligible})
		}
		prev = lv
	}
	return pots
}

// Distribute 把 pots 按 winnersByPot 分发,返回每人赢得的总额。
//
// 规则:
//   - winnersByPot[i] 必须是 pots[i].Eligible 的子集
//   - 平局余数给 winners 中顺位最先(seat 升序)
//   - 某层 winnersByPot[i] 为空(无人争,例如只有一人贡献到该层且他争)
//     → 金额发给 Eligible 里顺位最先(实质就是该唯一贡献者)
//
// 返回切片长度 = 最大 seat 索引 + 1。未参与任何 pot 的 seat 默认 0。
func Distribute(pots []Pot, winnersByPot [][]int) []int {
	if len(pots) != len(winnersByPot) {
		panic("sidepot: pots and winnersByPot length mismatch")
	}
	maxSeat := -1
	for _, p := range pots {
		for _, s := range p.Eligible {
			if s > maxSeat {
				maxSeat = s
			}
		}
	}
	result := make([]int, maxSeat+1)
	for i, pot := range pots {
		winners := append([]int(nil), winnersByPot[i]...)
		if len(winners) == 0 {
			// 无人争:发给 eligible 顺位最先(就是唯一争者或唯一贡献者)
			if len(pot.Eligible) > 0 {
				result[pot.Eligible[0]] += pot.Amount
			}
			continue
		}
		sort.Ints(winners) // 余数给顺位最先
		share := pot.Amount / len(winners)
		rem := pot.Amount - share*len(winners)
		for j, w := range winners {
			add := share
			if j == 0 {
				add += rem
			}
			result[w] += add
		}
	}
	return result
}
