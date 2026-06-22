// Package elo 实现标准 ELO rating 算法。
// 用于跨局累积每个 LLM 模型的相对强弱。
package elo

import "math"

// DefaultK 是默认 K 因子(控制单局 rating 波动幅度)。
const DefaultK = 32.0

// DefaultInitial 是新玩家的初始 rating。
const DefaultInitial = 1500.0

// Expected 返回 A 对 B 的预期胜率(0-1)。
// 公式:E_A = 1 / (1 + 10^((R_B - R_A)/400))
func Expected(ratingA, ratingB float64) float64 {
	return 1.0 / (1.0 + math.Pow(10, (ratingB-ratingA)/400.0))
}

// Score 是实际结局,A 的视角。
type Score float64

const (
	Win  Score = 1.0
	Loss Score = 0.0
	Draw Score = 0.5
)

// Update 按一局结果更新双方 rating,返回新 rating。
// K <= 0 时用 DefaultK。
func Update(ratingA, ratingB float64, score Score, k float64) (newA, newB float64) {
	if k <= 0 {
		k = DefaultK
	}
	eA := Expected(ratingA, ratingB)
	eB := 1.0 - eA
	deltaA := k * (float64(score) - eA)
	deltaB := k * (float64(1.0-score) - eB)
	return ratingA + deltaA, ratingB + deltaB
}

// UpdateMulti 按一局 N 人桌结果更新 rating。
//
// 算法(赢家 vs 每个输家两两算,赢家取均值):
//   - 对每个输家 l:用 Expected(winner, l) 算两人 ELO 增量
//   - 赢家的新 rating = winnerRating + mean(对每个 l 的 winner 增量)
//   - 每个输家 l 的新 rating = l + (单独一次两两计算的 l 增量)
//
// 这是多人桌业界常见做法(避免赢家对单个输家的 ELO 变化被夸大)。
// 平局场景(没有真正赢家)留给上层处理 —— 本函数假定有单一明确赢家。
//
// loserRatings 为空时返回 (winnerRating 不变, nil)。
// k <= 0 时用 DefaultK。
func UpdateMulti(winnerRating float64, loserRatings []float64, k float64) (newWinner float64, newLosers []float64) {
	if k <= 0 {
		k = DefaultK
	}
	if len(loserRatings) == 0 {
		return winnerRating, nil
	}
	winnerDeltaSum := 0.0
	newLosers = make([]float64, len(loserRatings))
	for i, lr := range loserRatings {
		newW, newL := Update(winnerRating, lr, Win, k)
		winnerDeltaSum += (newW - winnerRating)
		newLosers[i] = newL
	}
	newWinner = winnerRating + winnerDeltaSum/float64(len(loserRatings))
	return newWinner, newLosers
}
