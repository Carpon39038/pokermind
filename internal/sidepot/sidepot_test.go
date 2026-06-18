package sidepot

import (
	"reflect"
	"testing"
)

// potsEqual 比较两批 Pot(忽略 Eligible 顺序不可控问题——这里 Eligible 已按升序)。
func potsEqual(a, b []Pot) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Amount != b[i].Amount {
			return false
		}
		if !reflect.DeepEqual(a[i].Eligible, b[i].Eligible) {
			return false
		}
	}
	return true
}

func TestComputeEmpty(t *testing.T) {
	if got := Compute(nil, nil); got != nil {
		t.Fatalf("nil input -> got %v, want nil", got)
	}
	if got := Compute([]int{0, 0}, []bool{true, true}); got != nil {
		t.Fatalf("zero contrib -> got %v, want nil", got)
	}
}

func TestComputeHeadsUpEqual(t *testing.T) {
	// 两人各 10,都争。阈值 [10],1 层 pot 20,eligible [0,1]
	pots := Compute([]int{10, 10}, []bool{true, true})
	want := []Pot{{Amount: 20, Eligible: []int{0, 1}}}
	if !potsEqual(pots, want) {
		t.Fatalf("got %+v, want %+v", pots, want)
	}
}

func TestComputeThreePlayersAllIn(t *testing.T) {
	// A=100, B=50, C=50, 都争
	// 阈值 [50,100]
	// lv=50, diff=50, contrib>=50 三人 → pot 150, eligible [0,1,2]
	// lv=100, diff=50, contrib>=100 只 A → pot 50, eligible [0]
	pots := Compute([]int{100, 50, 50}, []bool{true, true, true})
	want := []Pot{
		{Amount: 150, Eligible: []int{0, 1, 2}},
		{Amount: 50, Eligible: []int{0}},
	}
	if !potsEqual(pots, want) {
		t.Fatalf("got %+v, want %+v", pots, want)
	}
	// 总额守恒:150+50=200 = 100+50+50
	totalPot := 0
	for _, p := range pots {
		totalPot += p.Amount
	}
	if totalPot != 200 {
		t.Fatalf("total pot = %d, want 200", totalPot)
	}
}

func TestComputeFolderContributionStays(t *testing.T) {
	// A=100, B=50(弃牌), C=30,都投入,B 弃
	// 阈值 [30,50,100]
	// lv=30, diff=30, 三人 contrib → pot 90, eligible 只 [0,2](B 弃)
	// lv=50, diff=20, contrib>=50 只 A,B → pot 40, eligible [0](B 弃)
	// lv=100, diff=50, contrib>=100 只 A → pot 50, eligible [0]
	pots := Compute([]int{100, 50, 30}, []bool{true, false, true})
	want := []Pot{
		{Amount: 90, Eligible: []int{0, 2}},
		{Amount: 40, Eligible: []int{0}},
		{Amount: 50, Eligible: []int{0}},
	}
	if !potsEqual(pots, want) {
		t.Fatalf("got %+v, want %+v", pots, want)
	}
}

func TestComputeAllEqualSinglePot(t *testing.T) {
	// 4 人各 20,都争 → 1 层 pot 80
	pots := Compute([]int{20, 20, 20, 20}, []bool{true, true, true, true})
	want := []Pot{{Amount: 80, Eligible: []int{0, 1, 2, 3}}}
	if !potsEqual(pots, want) {
		t.Fatalf("got %+v, want %+v", pots, want)
	}
}

func TestDistributeEvenSplit(t *testing.T) {
	// pot 100, winners [1,3] → 各 50,无余数
	pots := []Pot{{Amount: 100, Eligible: []int{1, 3}}}
	got := Distribute(pots, [][]int{{1, 3}})
	want := []int{0, 50, 0, 50}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDistributeOddRemainderToFirstSeat(t *testing.T) {
	// pot 101, winners [1,3] → share=50, rem=1 → seat 1 拿 51, seat 3 拿 50
	pots := []Pot{{Amount: 101, Eligible: []int{1, 3}}}
	got := Distribute(pots, [][]int{{3, 1}}) // 故意乱序,验证排序后余数给最先
	want := []int{0, 51, 0, 50}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDistributeNoWinnersGoesToEligible(t *testing.T) {
	// 某层 winners 为空(只有一人贡献到该层且他争)
	// pot 50 eligible [0] winners [] → 全给 seat 0
	pots := []Pot{{Amount: 50, Eligible: []int{0}}}
	got := Distribute(pots, [][]int{nil})
	want := []int{50}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDistributeMultiPotScenario(t *testing.T) {
	// 复合:A=100 B=50 C=50 都争,A 赢主池(50×3=150)+ 边池(50) 总 200
	pots := Compute([]int{100, 50, 50}, []bool{true, true, true})
	// 假设 A 在两层都赢
	got := Distribute(pots, [][]int{{0}, {0}})
	// A 拿主池 150 + 边池 50 = 200;B、C 各 0
	want := []int{200, 0, 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDistributeSplitPotBetweenTwoWinners(t *testing.T) {
	// A=100 B=100 C=100 都争,平局 A vs B(都最强),C 输
	// 1 层 pot 300, winners [0,1] → 各 150
	pots := Compute([]int{100, 100, 100}, []bool{true, true, true})
	got := Distribute(pots, [][]int{{0, 1}})
	want := []int{150, 150, 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
