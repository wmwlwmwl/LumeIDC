package cron

import (
	"sort"
	"testing"
)

func TestSplitByPresence(t *testing.T) {
	sorted := func(v []int64) []int64 {
		out := append([]int64(nil), v...)
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	eq := func(a, b []int64) bool {
		sa, sb := sorted(a), sorted(b)
		if len(sa) != len(sb) {
			return false
		}
		for i := range sa {
			if sa[i] != sb[i] {
				return false
			}
		}
		return true
	}

	cases := []struct {
		name            string
		pids            map[int64]int64 // upstreamPID -> localID
		present         map[int64]bool  // localID
		wantOff, wantOn []int64
	}{
		{"全部命中", map[int64]int64{11: 1, 22: 2}, map[int64]bool{1: true, 2: true}, nil, []int64{1, 2}},
		{"全部缺失", map[int64]int64{11: 1, 22: 2}, map[int64]bool{}, []int64{1, 2}, nil},
		{"部分缺失", map[int64]int64{11: 1, 22: 2, 33: 3}, map[int64]bool{2: true}, []int64{1, 3}, []int64{2}},
		{"空输入", map[int64]int64{}, map[int64]bool{9: true}, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			off, on := splitByPresence(c.pids, c.present)
			if !eq(off, c.wantOff) {
				t.Fatalf("停售分组不符：期望 %v，实际 %v", c.wantOff, off)
			}
			if !eq(on, c.wantOn) {
				t.Fatalf("在售分组不符：期望 %v，实际 %v", c.wantOn, on)
			}
		})
	}
}
