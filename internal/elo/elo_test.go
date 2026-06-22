package elo

import (
	"math"
	"testing"
)

func TestExpectedSymmetric(t *testing.T) {
	// 同分时,预期胜率 0.5
	if got := Expected(1500, 1500); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("Expected(1500,1500) = %v, want 0.5", got)
	}
	// A 比 B 高 400 分,A 胜率约 0.909
	got := Expected(1900, 1500)
	if math.Abs(got-0.9091) > 0.001 {
		t.Fatalf("Expected(1900,1500) = %v, want ~0.909", got)
	}
	// 对称性:Expected(A,B) + Expected(B,A) = 1
	if a, b := Expected(1500, 1700), Expected(1700, 1500); math.Abs(a+b-1.0) > 1e-9 {
		t.Fatalf("symmetry broken: %v + %v = %v", a, b, a+b)
	}
}

func TestUpdateWin(t *testing.T) {
	// 平分平局双方不变;一方赢分,赢家涨、输家跌,且和=守恒
	newA, newB := Update(1500, 1500, Win, 32)
	if newA <= 1500 {
		t.Fatalf("winner rating %v should increase", newA)
	}
	if newB >= 1500 {
		t.Fatalf("loser rating %v should decrease", newB)
	}
	// 守恒:delta 量级相同方向相反
	if math.Abs((newA-1500)+(newB-1500)) > 1e-9 {
		t.Fatalf("rating changes not conserved: %+v / %+v", newA-1500, newB-1500)
	}
}

func TestUpdateDraw(t *testing.T) {
	newA, newB := Update(1500, 1500, Draw, 32)
	if math.Abs(newA-1500) > 1e-9 || math.Abs(newB-1500) > 1e-9 {
		t.Fatalf("equal-rating draw should leave ratings unchanged: %v %v", newA, newB)
	}
}

func TestUpdateAsymmetricRatings(t *testing.T) {
	// 强者(1900)赢弱者(1500):因为预期胜率 0.909,实际赢的增益小
	newA, _ := Update(1900, 1500, Win, 32)
	gain := newA - 1900
	if gain > 5 {
		t.Fatalf("favorite's win gain %v should be small (<5)", gain)
	}
	// 弱者爆冷赢:增益应该大
	newA2, _ := Update(1500, 1900, Win, 32)
	if newA2-1500 < 20 {
		t.Fatalf("underdog's win gain %v should be large (>20)", newA2-1500)
	}
}

func TestUpdateUsesDefaultKWhenZero(t *testing.T) {
	// k=0 应使用 DefaultK=32,结果与显式 k=32 相同
	a1, b1 := Update(1500, 1500, Win, 0)
	a2, b2 := Update(1500, 1500, Win, 32)
	if math.Abs(a1-a2) > 1e-9 || math.Abs(b1-b2) > 1e-9 {
		t.Fatalf("k=0 should equal k=32: (%v,%v) vs (%v,%v)", a1, b1, a2, b2)
	}
}

func TestUpdateMultiEmptyLosers(t *testing.T) {
	// 无输家:赢家 rating 不变,返回 nil
	w, l := UpdateMulti(1500, nil, 32)
	if w != 1500 || l != nil {
		t.Fatalf("empty losers: (%v, %v), want (1500, nil)", w, l)
	}
}

func TestUpdateMultiSingleLoserMatchesTwoPlayer(t *testing.T) {
	// 单个输家:应与两两 Update 完全一致(均值退化为单值)
	w, l := UpdateMulti(1500, []float64{1500}, 32)
	w2, l2 := Update(1500, 1500, Win, 32)
	if math.Abs(w-w2) > 1e-9 || math.Abs(l[0]-l2) > 1e-9 {
		t.Fatalf("single loser should match Update: got (%v, %v) vs (%v, %v)", w, l[0], w2, l2)
	}
}

func TestUpdateMultiWinnerGainsLosersLose(t *testing.T) {
	// 三人桌:赢家 1500 vs 两输家 1500/1500。赢家应涨,两输家应跌
	w, l := UpdateMulti(1500, []float64{1500, 1500}, 32)
	if w <= 1500 {
		t.Fatalf("winner should gain, got %v", w)
	}
	for _, nl := range l {
		if nl >= 1500 {
			t.Fatalf("each loser should drop, got %v", nl)
		}
	}
}

func TestUpdateMultiWinnerTakesAverage(t *testing.T) {
	// 关键性质:赢家的增量 = 两个两两增量的均值,不是和
	// 1500 vs 1500:两两赢家增量 = K×(1-0.5) = 16
	// 1500 vs 1700:对手强 → 赢家爆冷增益更大(>16)
	// 均值应介于两者之间
	w, _ := UpdateMulti(1500, []float64{1500, 1700}, 32)
	wWeak, _ := Update(1500, 1500, Win, 32)
	wStrong, _ := Update(1500, 1700, Win, 32)
	gainWeak := wWeak - 1500
	gainStrong := wStrong - 1500
	gain := w - 1500
	lo, hi := gainWeak, gainStrong
	if hi < lo {
		lo, hi = hi, lo
	}
	if gain <= lo || gain >= hi {
		t.Fatalf("multi winner gain %v should be strictly between (%v, %v)", gain, lo, hi)
	}
	// 均值检验
	mid := (gainWeak + gainStrong) / 2
	if math.Abs(gain-mid) > 1e-9 {
		t.Fatalf("multi gain %v should equal mean %v", gain, mid)
	}
}

func TestUpdateMultiUsesDefaultKWhenZero(t *testing.T) {
	// k=0 应等价于 k=DefaultK
	w1, l1 := UpdateMulti(1500, []float64{1500, 1500}, 0)
	w2, l2 := UpdateMulti(1500, []float64{1500, 1500}, 32)
	if math.Abs(w1-w2) > 1e-9 || math.Abs(l1[0]-l2[0]) > 1e-9 {
		t.Fatalf("k=0 should equal k=32: (%v,%v) vs (%v,%v)", w1, l1, w2, l2)
	}
}
