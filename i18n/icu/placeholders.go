package icu

import "sort"

// Arguments returns every argument name in m, including plural and select
// names and names used inside options and tag children.
func Arguments(m Message) map[string]struct{} {
	set := make(map[string]struct{})
	collectArguments(m, set)
	return set
}

func collectArguments(nodes []Node, set map[string]struct{}) {
	for _, n := range nodes {
		switch n.Kind {
		case KindArgument:
			set[n.Name] = struct{}{}
		case KindPlural, KindSelect:
			set[n.Name] = struct{}{}
			for _, o := range n.Options {
				collectArguments(o.Value, set)
			}
		case KindTag:
			collectArguments(n.Children, set)
		}
	}
}

// Tags returns every tag name in m, recursively.
func Tags(m Message) map[string]struct{} {
	set := make(map[string]struct{})
	collectTags(m, set)
	return set
}

func collectTags(nodes []Node, set map[string]struct{}) {
	for _, n := range nodes {
		switch n.Kind {
		case KindTag:
			set[n.Name] = struct{}{}
			collectTags(n.Children, set)
		case KindPlural, KindSelect:
			for _, o := range n.Options {
				collectTags(o.Value, set)
			}
		}
	}
}

// SortedKeys returns the members of set in ascending order.
func SortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
