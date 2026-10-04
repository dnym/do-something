package scoring

import (
	"dosomething/internal/model"
	"testing"
)

func TestOngoingScoringOmitsDuration(t *testing.T) {
	for _, kind := range model.AllKinds {
		t.Run(string(kind), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Groups = map[model.Group]map[model.Kind][]model.Property{
				model.GroupValue:      {kind: {model.PropInterest}},
				model.GroupInterest:   {kind: {model.PropQuality}},
				model.GroupCommitment: {kind: {model.PropRemainingDuration, model.PropTotalDuration}},
			}
			it := mkItem(kind, func(i *model.Item) {
				i.Ongoing = true
				i.Ratings = map[model.Property]model.Rating{model.PropInterest: {Value: 1}, model.PropQuality: {Value: 0}}
			})
			result, err := Score(it, Context{}, cfg, testNow)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Groups) != 2 || result.Base != .5 {
				t.Fatal(result)
			}
			for _, g := range result.Groups {
				if g.Share != .5 || g.Group == model.GroupCommitment {
					t.Fatal(g)
				}
			}
			// A nonempty commitment group still contributes its applicable cost.
			cfg.Groups[model.GroupCommitment][kind] = []model.Property{model.PropRemainingDuration, model.PropCostLeft}
			cfg.K[model.PropCostLeft] = 100
			it.CostLeft = model.FloatPtr(0)
			result, err = Score(it, Context{}, cfg, testNow)
			if err != nil {
				t.Fatal(err)
			}
			want(t, "cost retained", result.Base, 2.0/3)
			for _, g := range result.Groups {
				if g.Group == model.GroupCommitment && (len(g.Members) != 1 || g.Members[0].Property != model.PropCostLeft) {
					t.Fatal(g)
				}
			}
		})
	}
}
