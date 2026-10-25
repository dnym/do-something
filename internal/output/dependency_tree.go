package output

import (
	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"fmt"
	"io"
	"strings"
)

type DependencyTreeItem struct {
	ID     string       `json:"id"`
	Title  string       `json:"title"`
	Status model.Status `json:"status"`
}

// Rows are in depth-first display order. Depth zero attaches to Selected;
// otherwise ParentID identifies the immediately enclosing item. Context rows
// (also_requires) are leaves, even when the task has further dependencies.
type DependencyTreeRow struct {
	Item      DependencyTreeItem `json:"item"`
	ParentID  string             `json:"parent_id"`
	Depth     int                `json:"depth"`
	Relation  string             `json:"relation"`
	Reference bool               `json:"reference"`
}

type DependencyTree struct {
	SchemaVersion int                 `json:"schema_version"`
	Selected      DependencyTreeItem  `json:"selected"`
	Prerequisites []DependencyTreeRow `json:"prerequisites"`
	Dependents    []DependencyTreeRow `json:"dependents"`
	AllIDs        []string            `json:"-"`
}

func DependencyTreeText(w io.Writer, r DependencyTree, tr *i18n.Translator) error {
	label := func(it DependencyTreeItem) string {
		return tr.T("output.tree.item", "title", it.Title, "id", Prefix(it.ID, r.AllIDs), "status", it.Status)
	}
	if _, err := fmt.Fprintln(w, label(r.Selected)+" "+tr.T("output.tree.selected")); err != nil {
		return err
	}
	for _, section := range []struct {
		key  string
		rows []DependencyTreeRow
	}{
		{"output.tree.prerequisites", r.Prerequisites}, {"output.tree.dependents", r.Dependents},
	} {
		if _, err := fmt.Fprintln(w, tr.T(section.key)); err != nil {
			return err
		}
		if len(section.rows) == 0 {
			if _, err := fmt.Fprintln(w, "  "+tr.T("output.none")); err != nil {
				return err
			}
		}
		// A stack of connector columns keeps rendering linear in the number of
		// rows plus the output size, including wide and deeply nested graphs.
		last := make([]bool, len(section.rows))
		stack := []int{}
		for i, row := range section.rows {
			for len(stack) > 0 && section.rows[stack[len(stack)-1]].Depth >= row.Depth {
				previous := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				last[previous] = section.rows[previous].Depth > row.Depth
			}
			stack = append(stack, i)
		}
		for _, i := range stack {
			last[i] = true
		}
		columns := []string{}
		for i, row := range section.rows {
			columns = columns[:row.Depth]
			connector, continuation := "├─ ", "│  "
			if last[i] {
				connector, continuation = "└─ ", "   "
			}
			text := strings.Join(columns, "") + connector + tr.T("output.tree."+row.Relation) + " " + label(row.Item)
			if row.Reference {
				text += " " + tr.T("output.tree.reference")
			}
			if _, err := fmt.Fprintln(w, text); err != nil {
				return err
			}
			columns = append(columns, continuation)
		}
	}
	return nil
}
