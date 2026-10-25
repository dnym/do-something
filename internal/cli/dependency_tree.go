package cli

import (
	"dosomething/internal/model"
	"dosomething/internal/output"
	"dosomething/internal/store"
	"sort"
	"strings"
)

func dependencyTree(s *store.Store, id string) (output.DependencyTree, error) {
	items, err := s.AllItems()
	if err != nil {
		return output.DependencyTree{}, err
	}
	return buildDependencyTree(items, id), nil
}

// Build each direction independently. Context leaves never expand or suppress a
// task's later expansion along the selected task's actual dependency paths.
func buildDependencyTree(items []*model.Item, id string) output.DependencyTree {
	r := output.DependencyTree{SchemaVersion: output.SchemaVersion, Prerequisites: []output.DependencyTreeRow{}, Dependents: []output.DependencyTreeRow{}}
	byID := map[string]*model.Item{}
	dependents := map[string][]string{}
	prerequisites := map[string][]string{}
	for _, it := range items {
		byID[it.ID] = it
		r.AllIDs = append(r.AllIDs, it.ID)
		prerequisites[it.ID] = append([]string{}, it.Dependencies...)
		for _, dep := range it.Dependencies {
			dependents[dep] = append(dependents[dep], it.ID)
		}
	}
	less := func(a, b string) bool {
		ta, tb := strings.ToLower(byID[a].Title), strings.ToLower(byID[b].Title)
		if ta == tb {
			return a < b
		}
		return ta < tb
	}
	for _, edges := range []map[string][]string{prerequisites, dependents} {
		for _, ids := range edges {
			sort.Slice(ids, func(i, j int) bool { return less(ids[i], ids[j]) })
		}
	}
	item := func(id string) output.DependencyTreeItem {
		it := byID[id]
		return output.DependencyTreeItem{ID: it.ID, Title: it.Title, Status: it.Status}
	}
	r.Selected = item(id)
	shown := map[string]bool{id: true}
	var up func(string, int)
	up = func(parent string, depth int) {
		for _, dep := range prerequisites[parent] {
			ref := shown[dep]
			r.Prerequisites = append(r.Prerequisites, output.DependencyTreeRow{Item: item(dep), ParentID: parent, Depth: depth, Relation: "requires", Reference: ref})
			shown[dep] = true
			if !ref {
				up(dep, depth+1)
			}
		}
	}
	up(id, 0)
	// A context leaf referring to a downstream task points to that task's main
	// occurrence, even if the context appears before its expansion.
	downstream := map[string]bool{id: true}
	queue := []string{id}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range dependents[current] {
			if !downstream[next] {
				downstream[next] = true
				queue = append(queue, next)
			}
		}
	}
	expanded := map[string]bool{id: true}
	var down func(string, int)
	down = func(parent string, depth int) {
		for _, dep := range dependents[parent] {
			ref := expanded[dep]
			r.Dependents = append(r.Dependents, output.DependencyTreeRow{Item: item(dep), ParentID: parent, Depth: depth, Relation: "dependent", Reference: ref})
			if ref {
				continue
			}
			expanded[dep], shown[dep] = true, true
			for _, sibling := range prerequisites[dep] {
				if sibling == parent {
					continue
				}
				r.Dependents = append(r.Dependents, output.DependencyTreeRow{Item: item(sibling), ParentID: dep, Depth: depth + 1, Relation: "also_requires", Reference: shown[sibling] || downstream[sibling]})
				shown[sibling] = true
			}
			down(dep, depth+1)
		}
	}
	down(id, 0)
	return r
}
