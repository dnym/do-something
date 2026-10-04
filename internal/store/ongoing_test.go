package store

import (
	"dosomething/internal/model"
	"encoding/json"
	"testing"
)

func TestOngoingContentValidation(t *testing.T) {
	s := openTestStore(t)
	id := addTestItem(t, s, "Activity", model.KindProject)
	if _, err := s.Save(ItemSpec{ID: id, RemainingDuration: model.FloatPtr(1)}, false); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{int64(1), int64(2), json.Number("0.5"), true} {
		c, err := s.Content()
		if err != nil {
			t.Fatal(err)
		}
		c.Items[0]["ongoing"] = value
		if err = c.Validate(); err == nil {
			t.Fatalf("accepted invalid ongoing content: %v", value)
		}
		if err = s.ApplyContent(c); err == nil {
			t.Fatal("applied invalid content")
		}
		it, err := s.GetItem(id)
		if err != nil || it.Ongoing || it.RemainingDuration == nil || *it.RemainingDuration != 1 {
			t.Fatal("invalid content changed store", it, err)
		}
	}
	c, err := s.Content()
	if err != nil {
		t.Fatal(err)
	}
	delete(c.Items[0], "ongoing")
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeContent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Items[0]["ongoing"] != int64(0) {
		t.Fatal("omitted ongoing must normalize to false", decoded)
	}
}
