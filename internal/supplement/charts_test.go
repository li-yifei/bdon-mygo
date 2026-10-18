package supplement

import (
	"reflect"
	"testing"
)

func tick(v float64) *float64 { return &v }

// A slide 2→4 with ticks 101 (on note 3's time) and 102, 103 (between notes):
// 101 follows note 3's group; the others follow the slide's end, by time,
// each with the other lines' ticks at its exact time.
func TestGameOrderFollowsTheClient(t *testing.T) {
	s := liveScore{
		Notes: []liveNote{
			{ID: 1, JudgementType: "Normal", TimeMs: 100, Tick: tick(0)},
			{ID: 2, JudgementType: "SlideBegin", TimeMs: 200, Tick: tick(10)},
			{ID: 3, JudgementType: "Normal", TimeMs: 300, Tick: tick(20)},
			{ID: 4, JudgementType: "SlideEnd", TimeMs: 400, Tick: tick(30)},
			{ID: 5, JudgementType: "SlideBegin", TimeMs: 250, Tick: tick(15)},
			{ID: 6, JudgementType: "SlideEnd", TimeMs: 500, Tick: tick(40)},
			{ID: 101, JudgementType: "Trace", TimeMs: 300},
			{ID: 102, JudgementType: "Trace", TimeMs: 350},
			{ID: 103, JudgementType: "Trace", TimeMs: 250},
			{ID: 201, JudgementType: "Trace", TimeMs: 350},
			{ID: 202, JudgementType: "Trace", TimeMs: 450},
		},
		Lines: []liveLine{{NoteIDs: []int{2, 4}, ComboIDs: []int{101, 102, 103}}, {NoteIDs: []int{5, 6}, ComboIDs: []int{201, 202}}},
	}
	order, err := gameOrder(s)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for _, n := range order {
		ids = append(ids, n.ID)
	}
	want := []int{1, 2, 3, 101, 4, 103, 102, 201, 5, 6, 202}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("order = %v, want %v", ids, want)
	}
}

func TestChartOfRejectsUnknownJudgements(t *testing.T) {
	s := liveScore{Notes: []liveNote{{ID: 1, JudgementType: "Mystery", Tick: tick(0)}}}
	if _, err := chartOf(1, s); err == nil {
		t.Fatal("accepted an unknown judgement type")
	}
}
